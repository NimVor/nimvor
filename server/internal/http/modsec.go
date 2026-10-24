package http

// This file ports the ModSecurity page (ModSecurityController: the
// server-wide index with its websites, events, vendors, custom-rules and
// installation tabs; the server-wide settings; the per-rule and bulk custom
// rule editing; the background install/update/rollback jobs) and a
// website's Web firewall page (its mode, the rules for it, and turning a
// rule off from an event). The agent is authoritative: every change is
// checked with nginx -t and rolled back on failure (agent side).

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/security"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// modsecDenied is the controller's denied(): the all-scope gate (the page and
// every administrator action).
func (s *Server) modsecDenied(w http.ResponseWriter) {
	renderStatus(w, s, "error", map[string]any{
		"Title":   "Access denied",
		"Message": "ModSecurity rules are managed by server administrators.",
	}, http.StatusForbidden)
}

// modsecCustomNote is the hint appended when the custom rules are saved but
// not in use yet (the server-wide switch is off).
const modsecCustomNote = ` Turn "Custom rules" on in the server-wide settings to use them.`

// modsecRetention is modsec_backup_retention_days (default 30) — how long
// installation backups and records are kept (SettingsService's default).
func (s *Server) modsecRetention(ctx context.Context) int {
	v, _ := s.settings.Int(ctx, "modsec_backup_retention_days", 30)
	return v
}

// agentCallError shapes a non-agent failure into an agent error (the pages
// render agent errors as a warning, as in PHP's catch (AgentException)).
func agentCallError(err error) *agent.Error {
	if err == nil {
		return nil
	}
	ae := &agent.Error{}
	if errors.As(err, &ae) {
		return ae
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &agent.Error{Code: "agent_timeout", Message: "The WhaCP agent took too long to answer."}
	}
	return &agent.Error{Code: "agent_unreachable", Message: "The WhaCP agent could not be reached."}
}

// modsecStatus is the agent's modsec.status (nil + the error on failure).
func (s *Server) modsecStatus(ctx context.Context) (map[string]any, *agent.Error) {
	res, err := s.agent.Call(ctx, "modsec.status", nil, 30*time.Second)
	if err != nil {
		return nil, agentCallError(err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, &agent.Error{Code: "agent_bad_response", Message: "The WhaCP agent answered with an unreadable state."}
	}
	return out, nil
}

// modsecCustomRules is the agent's modsec.custom_get (the rules text).
func (s *Server) modsecCustomRules(ctx context.Context) (string, *agent.Error) {
	res, err := s.agent.Call(ctx, "modsec.custom_get", nil, 30*time.Second)
	if err != nil {
		return "", agentCallError(err)
	}
	var out struct {
		Rules string `json:"rules"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", &agent.Error{Code: "agent_bad_response", Message: "The WhaCP agent answered with an unreadable state."}
	}
	return out.Rules, nil
}

// modsecEvents is the agent's modsec.events, all hosts or one website's
// (domain, www.domain, aliases — the service's events()).
func (s *Server) modsecEvents(ctx context.Context, site *hosting.Website, limit int) ([]map[string]any, *agent.Error) {
	params := map[string]any{"limit": limit}
	if site != nil {
		seen := map[string]bool{}
		hosts := []string{}
		add := func(name string) {
			name = strings.ToLower(name)
			if !seen[name] {
				seen[name] = true
				hosts = append(hosts, name)
			}
		}
		add(site.Domain)
		add("www." + site.Domain)
		for _, a := range site.Aliases {
			add(a)
		}
		params["hosts"] = hosts
	}
	res, err := s.agent.Call(ctx, "modsec.events", params, 30*time.Second)
	if err != nil {
		return nil, agentCallError(err)
	}
	var out struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, &agent.Error{Code: "agent_bad_response", Message: "The WhaCP agent answered with an unreadable state."}
	}
	return out.Events, nil
}

// modsecInstalls is the agent's modsec.installs (an older agent has no
// records: the caller treats any error as an empty list).
func (s *Server) modsecInstalls(ctx context.Context) []map[string]any {
	res, err := s.agent.Call(ctx, "modsec.installs", nil, 30*time.Second)
	if err != nil {
		return []map[string]any{}
	}
	var out struct {
		Installs []map[string]any `json:"installs"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return []map[string]any{}
	}
	if out.Installs == nil {
		return []map[string]any{}
	}
	return out.Installs
}

// modsecSaveCustomRules is saveCustomRules: the size cap, the agent's
// modsec.custom_set (nginx -t, rollback) and the audit row.
func (s *Server) modsecSaveCustomRules(ctx context.Context, r *http.Request, rules string, user *identity.User) (int, error) {
	if len(rules) > 65536 {
		return 0, &security.ModsecError{Message: "The rules are larger than 64 KiB."}
	}
	res, err := s.agent.Call(ctx, "modsec.custom_set", map[string]any{"rules": rules}, 120*time.Second)
	if err != nil {
		return 0, err
	}
	var out struct {
		Rules int `json:"rules"`
	}
	_ = json.Unmarshal(res, &out)
	sum := sha256.Sum256([]byte(rules))
	s.audit.Record(ctx, audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "modsec.custom_rules",
		TargetType: "server",
		TargetID:   strconv.FormatInt(s.currentServerID(r), 10),
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{"rules": out.Rules, "sha256": hex.EncodeToString(sum[:])},
	})
	return out.Rules, nil
}

// modsecCustomInUse is the page's "custom rules on?" check (the server-wide
// config from a fresh status()).
func (s *Server) modsecCustomInUse(ctx context.Context) (bool, *agent.Error) {
	status, ae := s.modsecStatus(ctx)
	if ae != nil {
		return false, ae
	}
	cfg, _ := status["config"].(map[string]any)
	custom, _ := cfg["custom"].(map[string]any)
	on, _ := custom["enabled"].(bool)
	return on, nil
}

// modsecSetSite is setSite: only administrators change the vendors and the
// rules; everyone else keeps what is set and changes the mode only (a nil
// argument keeps the current value, as in PHP's ?array).
func (s *Server) modsecSetSite(ctx context.Context, r *http.Request, site *hosting.Website, mode string, exclude []string, disabled *[]int, admin bool, user *identity.User) error {
	if !security.ModsecModeKnown(mode) {
		return &security.ModsecError{Message: "Choose off, detection only or on."}
	}
	status, ae := s.modsecStatus(ctx)
	if ae != nil {
		return ae
	}
	cur := security.ModsecSettingFor(status["sites"], site.Domain)
	if !admin || exclude == nil {
		exclude = cur.Exclude
	}
	var dis []int
	if admin && disabled != nil {
		dis = *disabled
	} else {
		dis = cur.DisabledRules
	}
	kept := []string{}
	for _, v := range security.ModsecVendorIDs {
		for _, e := range exclude {
			if v == e {
				kept = append(kept, v)
				break
			}
		}
	}
	exclude = kept
	seen := map[int]bool{}
	clean := []int{}
	for _, d := range dis {
		if d > 0 && d <= 9999999 && !seen[d] {
			seen[d] = true
			clean = append(clean, d)
		}
	}
	if len(clean) > 200 {
		return &security.ModsecError{Message: "At most 200 rules can be turned off for a website."}
	}
	params := map[string]any{
		"domain":         site.Domain,
		"mode":           mode,
		"exclude":        exclude,
		"disabled_rules": clean,
	}
	if _, err := s.agent.Call(ctx, "modsec.site", params, 120*time.Second); err != nil {
		return err
	}
	s.audit.Record(ctx, audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "modsec.site",
		TargetType: "website",
		TargetID:   strconv.FormatInt(site.ID, 10),
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    params,
	})
	return nil
}

// modsecServerID is the server's id as a string (the audit's target).
func (s *Server) modsecServerID(r *http.Request) string {
	return strconv.FormatInt(s.currentServerID(r), 10)
}

// ---- the index page ----

// modsecIndex renders the ModSecurity page (index — security.view, the
// all-scope gate).
func (s *Server) modsecIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.modsecDenied(w)
		return
	}
	ctx := r.Context()
	sess := sessionFrom(r.Context())

	// status, custom rules and the recent events: the first agent error
	// stops the chain (PHP's single try/catch).
	var status map[string]any
	var agentMsg string
	var custom string
	var events []map[string]any
	if st, ae := s.modsecStatus(ctx); ae != nil {
		agentMsg = ae.Message
	} else {
		status = st
		if c, ae := s.modsecCustomRules(ctx); ae != nil {
			agentMsg = ae.Message
		} else {
			custom = c
			if ev, ae := s.modsecEvents(ctx, nil, 50); ae != nil {
				agentMsg = ae.Message
			} else {
				events = ev
			}
		}
	}

	rep := hosting.NewWebsiteRepository(s.db)
	sites, err := rep.All(ctx, s.currentServerID(r), user)
	if err != nil {
		s.log.Error("list websites", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ids := make([]int64, 0, len(sites))
	for _, w := range sites {
		ids = append(ids, w.ID)
	}
	aliasMap, _ := rep.AliasesFor(ctx, ids)

	byDomain := map[string]int64{}
	for _, w := range sites {
		d := strings.ToLower(w.Domain)
		byDomain[d] = w.ID
		for _, a := range aliasMap[w.ID] {
			byDomain[strings.ToLower(a)] = w.ID
		}
		if _, ok := byDomain["www."+d]; !ok {
			byDomain["www."+d] = w.ID
		}
	}

	vendors := []security.ModsecVendorInfo{}
	if status != nil {
		vendors = modsecVendors(status)
	}
	installRows := modsecInstallRows(s.modsecInstalls(ctx))

	data := map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"CanManage":  false,
		"AgentError": agentMsg,
		"Status":     nil,
		"Custom":     custom,
		"Rules":      []security.ModsecRule{},
		"NextID":     10001,
		"Sites":      []modsecSiteRow{},
		"Events":     []modsecEventRow{},
		"Vendors":    []modsecVendorRow{},
		"Installs":   installRows,
		"Retention":  s.modsecRetention(ctx),
		"Pending":    s.securityPending(ctx),
	}
	if ok, _ := s.authorizer().Can(ctx, user, "security.manage"); ok {
		data["CanManage"] = true
	}
	data["RuleModals"] = []modsecRuleModal{}
	if status != nil {
		sv := modsecStatusViewFrom(status)
		data["Status"] = sv
		rawRules := security.ModsecSplitRules(custom)
		data["Rules"] = modsecRuleRows(rawRules)
		nextID := security.ModsecNextRuleID(rawRules)
		data["NextID"] = nextID
		data["Sites"] = modsecSiteRows(sites, status, vendors)
		ev := modsecEventRows(events, byDomain, true, 0)
		data["Events"] = ev
		data["EventCtx"] = modsecEventsCtx{Events: ev, ShowHost: true, CSRF: session.CSRFToken(sess)}
		data["Vendors"] = modsecVendorRows(status, vendors, sv)
		// The rule modals: "New rule" (the suggested directive) and one per
		// rule (the PHP's $ruleForm closure).
		if data["CanManage"] == true {
			modals := []modsecRuleModal{{
				MID: "ms-rule-new", Title: "New custom rule", Index: -1,
				Comment: "",
				Rule:    `SecRule REQUEST_URI "@beginsWith /example" "id:` + strconv.Itoa(nextID) + `,phase:1,deny,status:403,log,msg:'example'"`,
				NextID:  nextID,
				CSRF:    session.CSRFToken(sess),
			}}
			for i, rr := range rawRules {
				title := "Custom rule"
				if rr.ID != nil {
					title += " " + strconv.Itoa(*rr.ID)
				}
				modals = append(modals, modsecRuleModal{
					MID:     "ms-rule-" + strconv.Itoa(i),
					Title:   title,
					Index:   i,
					SHA:     modsecRuleSHA(rr.Text),
					Comment: rr.Comment,
					Rule:    rr.Text,
					NextID:  nextID,
					CSRF:    session.CSRFToken(sess),
				})
			}
			data["RuleModals"] = modals
		}
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	s.render(w, "security_modsec", data)
}

// modsecRuleModal is one rule form modal (the PHP's $ruleForm output).
type modsecRuleModal struct {
	MID     string
	Title   string
	Index   int // -1 for the "New rule" modal (no index/sha fields)
	SHA     string
	Comment string
	Rule    string
	NextID  int
	CSRF    string
}

// modsecRuleRow is one row of the custom rules table (plus its edit modal).
type modsecRuleRow struct {
	ID        string // "" when the rule has no id
	Comment   string
	Phase     string
	Action    string
	Msg       string
	Text      string
	TextShort string // one line, at most 160 characters (the view's trim)
	IsBlock   bool   // deny/block/drop (the red badge)
	SHA       string // sha256 of Text — the edit/delete form's optimistic lock
}

func modsecRuleRows(rules []security.ModsecRule) []modsecRuleRow {
	rows := make([]modsecRuleRow, 0, len(rules))
	for _, r := range rules {
		row := modsecRuleRow{
			Comment:   r.Comment,
			Phase:     r.Phase,
			Action:    r.Action,
			Msg:       r.Msg,
			Text:      r.Text,
			TextShort: strimwidth(collapseWhitespace(r.Text), 160),
			SHA:       modsecRuleSHA(r.Text),
		}
		if r.ID != nil {
			row.ID = strconv.Itoa(*r.ID)
		}
		switch r.Action {
		case "deny", "block", "drop":
			row.IsBlock = true
		}
		rows = append(rows, row)
	}
	return rows
}

// modsecRuleSHA is the form's sha field (the page's hash('sha256', text)).
func modsecRuleSHA(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// collapseWhitespace is preg_replace('/\s+/', ' ', text).
func collapseWhitespace(s string) string {
	var b strings.Builder
	prev := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == 0x0B || r == 0x0C {
			prev = true
			continue
		}
		if prev && b.Len() > 0 {
			b.WriteByte(' ')
		}
		prev = false
		b.WriteRune(r)
	}
	return b.String()
}

// modsecEventsCtx is the shared events table's data (the partial's
// $events/$byDomain/$showHost/$disableFor).
type modsecEventsCtx struct {
	Events        []modsecEventRow
	ShowHost      bool
	TurnOffID     int64
	TurnOffDomain string
	CSRF          string
}

// modsecStatusView is the agent's status for the template.
type modsecStatusView struct {
	ConnState    string
	ConnReason   string
	ConnModule   string
	ConnPackage  string
	NginxVersion string
	CRSAvailable bool
	CRSVersion   string
	ModuleLoaded bool
	Active       bool
	AuditBytes   string
	CustomCount  int
	Config       modsecConfigView
}

type modsecConfigView struct {
	Enabled       bool
	CRSEnabled    bool
	Paranoia      int
	Inbound       int
	Outbound      int
	DetectionOnly bool
	CustomEnabled bool
}

func modsecStatusViewFrom(status map[string]any) *modsecStatusView {
	sv := &modsecStatusView{ConnState: "missing", Config: modsecConfigView{Paranoia: 1, Inbound: 5, Outbound: 4}}
	if loaded, ok := status["module_loaded"].(bool); ok {
		sv.ModuleLoaded = loaded
	}
	conn, _ := status["connector"].(map[string]any)
	if conn != nil {
		if st, ok := conn["state"].(string); ok && st != "" {
			sv.ConnState = st
		} else {
			sv.ConnState = "missing"
			if sv.ModuleLoaded {
				sv.ConnState = "functional"
			}
		}
		sv.ConnReason, _ = conn["reason"].(string)
		sv.ConnPackage, _ = conn["package"].(string)
		sv.NginxVersion, _ = conn["nginx_version"].(string)
		if modules, ok := conn["modules"].([]any); ok && len(modules) > 0 {
			if m0, ok := modules[0].(map[string]any); ok {
				sv.ConnModule, _ = m0["path"].(string)
			}
		}
	} else if sv.ModuleLoaded {
		sv.ConnState = "functional"
	}
	if crs, ok := status["crs"].(map[string]any); ok {
		sv.CRSAvailable, _ = crs["available"].(bool)
		sv.CRSVersion, _ = crs["version"].(string)
	}
	if cfg, ok := status["config"].(map[string]any); ok {
		sv.Config.Enabled, _ = cfg["enabled"].(bool)
		sv.Config.DetectionOnly, _ = cfg["detection_only"].(bool)
		if c, ok := cfg["crs"].(map[string]any); ok {
			sv.Config.CRSEnabled, _ = c["enabled"].(bool)
			if p, ok := c["paranoia"].(float64); ok {
				sv.Config.Paranoia = int(p)
			}
			if v, ok := c["inbound_threshold"].(float64); ok {
				sv.Config.Inbound = int(v)
			}
			if v, ok := c["outbound_threshold"].(float64); ok {
				sv.Config.Outbound = int(v)
			}
		}
		if c, ok := cfg["custom"].(map[string]any); ok {
			sv.Config.CustomEnabled, _ = c["enabled"].(bool)
		}
	}
	sv.Active, _ = status["active"].(bool)
	if n, ok := status["audit_log_bytes"].(float64); ok {
		sv.AuditBytes = formatBytes(int64(n))
	} else {
		sv.AuditBytes = "0 B"
	}
	if n, ok := status["custom_rules"].(float64); ok {
		sv.CustomCount = int(n)
	}
	return sv
}

func modsecVendors(status map[string]any) []security.ModsecVendorInfo {
	out := []security.ModsecVendorInfo{}
	list, ok := status["vendors"].([]any)
	if !ok {
		return out
	}
	for _, v := range list {
		rm, ok := v.(map[string]any)
		if !ok {
			continue
		}
		info := security.ModsecVendorInfo{}
		info.ID, _ = rm["id"].(string)
		info.Name, _ = rm["name"].(string)
		info.Description, _ = rm["description"].(string)
		if n, ok := rm["first_id"].(float64); ok {
			info.FirstID = int(n)
		}
		if n, ok := rm["last_id"].(float64); ok {
			info.LastID = int(n)
		}
		out = append(out, info)
	}
	return out
}

// modsecSiteRow is one row of the websites tab (plus its edit modal).
type modsecSiteRow struct {
	ID            int64
	Domain        string
	OwnerName     string
	Mode          string
	ModeLabel     string
	ModeClass     string
	VendorsText   string
	DisabledText  string
	DisabledValue string
	ModeChecked   map[string]bool
	VendorChecked map[string]bool
}

func modsecSiteRows(sites []hosting.Website, status map[string]any, vendors []security.ModsecVendorInfo) []modsecSiteRow {
	rows := make([]modsecSiteRow, 0, len(sites))
	for _, w := range sites {
		set := security.ModsecSettingFor(status["sites"], w.Domain)
		row := modsecSiteRow{
			ID:            w.ID,
			Domain:        w.Domain,
			Mode:          set.Mode,
			ModeChecked:   map[string]bool{},
			VendorChecked: map[string]bool{},
		}
		if w.OwnerName.Valid {
			row.OwnerName = w.OwnerName.String
		}
		switch set.Mode {
		case "off":
			row.ModeLabel, row.ModeClass = "Off", "is-off"
		case "detect":
			row.ModeLabel, row.ModeClass = "Detection only", "is-warn"
		case "on":
			row.ModeLabel, row.ModeClass = "On", "is-ok"
		default:
			row.ModeLabel, row.ModeClass = "Off", "is-off"
		}
		excluded := map[string]bool{}
		for _, e := range set.Exclude {
			excluded[e] = true
		}
		kept := []string{}
		for _, v := range security.ModsecVendorIDs {
			if !excluded[v] {
				kept = append(kept, security.ModsecVendorName(vendors, v))
			}
		}
		row.VendorsText = strings.Join(kept, ", ")
		if row.VendorsText == "" {
			row.VendorsText = "—"
		}
		if len(set.DisabledRules) > 0 {
			parts := make([]string, 0, len(set.DisabledRules))
			for _, d := range set.DisabledRules {
				parts = append(parts, strconv.Itoa(d))
			}
			row.DisabledText = strings.Join(parts, " ")
			row.DisabledValue = row.DisabledText
		} else {
			row.DisabledText = "—"
		}
		for _, m := range security.ModsecModes {
			row.ModeChecked[m[0]] = set.Mode == m[0]
		}
		for _, v := range security.ModsecVendorIDs {
			row.VendorChecked[v] = !excluded[v]
		}
		rows = append(rows, row)
	}
	return rows
}

// modsecEventRow is one row of the events table.
type modsecEventRow struct {
	Time     string
	TimeFull string
	Host     string
	Link     string
	Method   string
	URI      string
	ClientIP string
	Blocked  bool
	Status   int
	Rules    []modsecEventRule
}

type modsecEventRule struct {
	ID      string
	Message string
	TurnOff bool
}

// The anomaly-score summaries say a request was blocked, not why (the
// partial's $summary).
var modsecSummaryRuleIDs = map[string]bool{"949110": true, "959100": true, "980130": true, "980140": true, "980170": true}

// modsecEventRows builds the event rows. linkFor maps a host to its
// website's firewall page ("" = no link); turnOffID/turnOffDomain give the
// "turn off here" target (the website's page only).
func modsecEventRows(events []map[string]any, linkFor map[string]int64, showHost bool, turnOffID int64) []modsecEventRow {
	rows := make([]modsecEventRow, 0, len(events))
	for _, ev := range events {
		row := modsecEventRow{Rules: []modsecEventRule{}}
		row.TimeFull, _ = ev["time"].(string)
		row.Time = timeAgo(row.TimeFull)
		row.Host, _ = ev["host"].(string)
		row.Method, _ = ev["method"].(string)
		if uri, ok := ev["uri"].(string); ok {
			row.URI = strimwidth(uri, 80)
		}
		row.ClientIP, _ = ev["client_ip"].(string)
		st, _ := ev["status"].(float64)
		row.Status = int(st)
		engine, _ := ev["engine"].(string)
		row.Blocked = row.Status >= 400 && engine != "DetectionOnly"
		if showHost {
			if id, ok := linkFor[strings.ToLower(row.Host)]; ok {
				row.Link = "/sites/" + strconv.FormatInt(id, 10) + "/firewall"
			}
		}
		if list, ok := ev["rules"].([]any); ok {
			for _, rv := range list {
				rm, ok := rv.(map[string]any)
				if !ok {
					continue
				}
				id, _ := rm["id"].(string)
				if modsecSummaryRuleIDs[id] {
					continue
				}
				msg, _ := rm["message"].(string)
				n, _ := strconv.Atoi(id)
				row.Rules = append(row.Rules, modsecEventRule{
					ID:      id,
					Message: msg,
					TurnOff: turnOffID != 0 && n > 0,
				})
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// modsecVendorRow is one row of the vendors tab.
type modsecVendorRow struct {
	ID          string
	Name        string
	Version     string
	Description string
	FirstID     int
	LastID      int
	State       string
	StateClass  string
}

func modsecVendorRows(status map[string]any, vendors []security.ModsecVendorInfo, sv *modsecStatusView) []modsecVendorRow {
	rows := make([]modsecVendorRow, 0, len(vendors))
	for _, v := range vendors {
		row := modsecVendorRow{ID: v.ID, Name: v.Name, Description: v.Description, FirstID: v.FirstID, LastID: v.LastID}
		if v.ID == "crs" {
			row.Version = sv.CRSVersion
			if !sv.CRSAvailable {
				row.State, row.StateClass = "Not installed", "is-off"
			} else if sv.Config.CRSEnabled {
				row.State, row.StateClass = "Enabled", "is-ok"
			} else {
				row.State, row.StateClass = "Disabled", "is-off"
			}
		} else {
			if sv.Config.CustomEnabled && sv.CustomCount > 0 {
				row.State, row.StateClass = "Enabled", "is-ok"
			} else {
				row.State, row.StateClass = "Disabled", "is-off"
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// modsecInstallRow is one row of the installation tab.
type modsecInstallRow struct {
	ID         string
	Started    string
	Method     string
	NginxVer   string
	OS         string
	Status     string
	StatusText string
	StatusCls  string
	InEffect   bool
	ErrShort   string
	ErrLong    bool
	ErrFull    string
	Folder     string
	Sources    []modsecSourceRow
	Module     string
}

type modsecSourceRow struct {
	Name    string
	Version string
	Origin  string
}

func modsecInstallRows(installs []map[string]any) []modsecInstallRow {
	rows := make([]modsecInstallRow, 0, len(installs))
	for _, in := range installs {
		if len(rows) >= 20 {
			break
		}
		row := modsecInstallRow{Sources: []modsecSourceRow{}}
		row.ID, _ = in["id"].(string)
		if started, ok := in["started_at"].(string); ok {
			if len(started) > 16 {
				started = started[:16]
			}
			row.Started = strings.ReplaceAll(started, "T", " ")
		}
		row.Method, _ = in["method"].(string)
		row.NginxVer, _ = in["nginx_version"].(string)
		row.OS, _ = in["os"].(string)
		row.Status, _ = in["status"].(string)
		switch row.Status {
		case "committed":
			row.StatusText, row.StatusCls = "Done", "is-ok"
		case "rolled_back":
			row.StatusText, row.StatusCls = "Rolled back", "is-warn"
		case "running":
			row.StatusText, row.StatusCls = "Running", "is-warn"
		default:
			row.StatusText, row.StatusCls = "Rollback incomplete", "is-error"
		}
		row.InEffect, _ = in["can_roll_back"].(bool)
		if e, ok := in["error"].(string); ok && e != "" {
			row.ErrShort, row.ErrLong = modsecErrorSummary(e)
			row.ErrFull = e
		}
		row.Folder, _ = in["folder"].(string)
		if list, ok := in["sources"].([]any); ok {
			for _, src := range list {
				rm, ok := src.(map[string]any)
				if !ok {
					continue
				}
				sr := modsecSourceRow{}
				sr.Name, _ = rm["name"].(string)
				sr.Version, _ = rm["version"].(string)
				sr.Origin, _ = rm["origin"].(string)
				row.Sources = append(row.Sources, sr)
			}
		}
		row.Module, _ = in["module"].(string)
		rows = append(rows, row)
	}
	return rows
}

// modsecErrorSummary is the view's errSummary(): a failed build's error
// carries the whole compiler output; the table shows its error lines (or its
// last lines), the rest behind "Show the full output".
func modsecErrorSummary(err string) (string, bool) {
	lines := []string{}
	for _, l := range strings.Split(strings.ReplaceAll(err, "\r\n", "\n"), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) <= 3 && len([]rune(err)) <= 400 {
		return err, false
	}
	errLine := regexp.MustCompile(`\b(error|failed|fatal|not found|cannot|no such)\b`)
	checking := regexp.MustCompile(`(?i)^checking\b`)
	hits := []string{}
	for _, l := range lines {
		if errLine.MatchString(l) && !checking.MatchString(l) {
			hits = append(hits, l)
		}
	}
	pick := hits
	if len(pick) == 0 {
		pick = lines
	}
	if len(pick) > 3 {
		pick = pick[len(pick)-3:]
	}
	short := []string{}
	for _, l := range pick {
		short = append(short, strimwidth(l, 220))
	}
	joined := strings.Join(short, " … ")
	if lines[0] != pick[0] {
		return strimwidth(lines[0], 160) + " — " + joined, true
	}
	return joined, true
}

// ---- the actions ----

// modsecPHPInt mirrors PHP's is_numeric + (int) cast for the form fields
// (the service's $num validator parses them as numbers before the range
// check).
func modsecPHPInt(v string) (int, bool) {
	t := strings.TrimSpace(v)
	if t == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil || f != f || f == math.Inf(1) || f == math.Inf(-1) {
		return 0, false
	}
	return int(f), true
}

// modsecConfigure applies the server-wide settings (configure —
// security.manage, administrators only).
func (s *Server) modsecConfigure(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.modsecDenied(w)
		return
	}
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	to := "/security/modsec"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
	// The defaults apply to an absent field only (PHP's $in[$field] ?? $def);
	// a present-but-empty value fails is_numeric, as in PHP.
	num := func(field, def string, lo, hi int, label string) (int, string) {
		v := r.FormValue(field)
		if !r.Form.Has(field) {
			v = def
		}
		n, ok := modsecPHPInt(v)
		if !ok || n < lo || n > hi {
			return 0, fmt.Sprintf("%s: %d to %d.", label, lo, hi)
		}
		return n, ""
	}
	paranoia, err1 := num("paranoia", "1", 1, 4, "Paranoia level")
	var inbound, outbound int
	if err1 == "" {
		inbound, err1 = num("inbound_threshold", "5", 2, 1000, "Inbound threshold")
	}
	if err1 == "" {
		outbound, err1 = num("outbound_threshold", "4", 2, 1000, "Outbound threshold")
	}
	if err1 != "" {
		fail(err1)
		return
	}
	cfg := map[string]any{
		"enabled": r.FormValue("enabled") == "1",
		"crs": map[string]any{
			"enabled":            r.FormValue("crs_enabled") == "1",
			"paranoia":           paranoia,
			"inbound_threshold":  inbound,
			"outbound_threshold": outbound,
		},
		"custom":         map[string]any{"enabled": r.FormValue("custom_enabled") == "1"},
		"detection_only": r.FormValue("detection_only") == "1",
	}
	if _, err := s.agent.Call(ctx, "modsec.configure", cfg, 120*time.Second); err != nil {
		agentErrorRedirect(w, r, "modsec configure", err, to)
		return
	}
	s.audit.Record(ctx, audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "modsec.configure",
		TargetType: "server",
		TargetID:   s.modsecServerID(r),
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    cfg,
	})
	if r.FormValue("enabled") == "1" {
		sess.Flash("success", "ModSecurity settings saved and loaded.")
	} else {
		sess.Flash("success", "ModSecurity is off: no website is filtered.")
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// modsecSaveRule adds, changes or deletes one custom rule (saveRule — the
// whole set is checked again).
func (s *Server) modsecSaveRule(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.modsecDenied(w)
		return
	}
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	to := "/security/modsec#custom"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}

	cur, ae := s.modsecCustomRules(ctx)
	if ae != nil {
		sess.Flash("error", ae.Message)
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	rules := security.ModsecSplitRules(cur)

	text := strings.TrimSpace(strings.ReplaceAll(r.FormValue("rule"), "\r\n", "\n"))
	comment := r.FormValue("comment")
	if rs := []rune(comment); len(rs) > 500 {
		comment = string(rs[:500])
	}
	comment = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(comment, "\r\n", "\n"), "\r", "\n"))

	index := r.FormValue("index")
	op := r.FormValue("op")
	var target *int
	if index != "" {
		i, _ := modsecPHPInt(index)
		if i < 0 || i >= len(rules) || !modsecSHAEquals(rules[i].Text, r.FormValue("sha")) {
			fail("The custom rules changed since the page was loaded. Reload the page and try again.")
			return
		}
		target = &i
	}
	if op == "delete" {
		if target == nil {
			fail("Choose a rule to delete.")
			return
		}
		rules = append(rules[:*target], rules[*target+1:]...)
		if _, err := s.modsecSaveCustomRules(ctx, r, security.ModsecJoinRules(rules), user); err != nil {
			modsecRuleFlashError(w, r, sess, to, err)
			return
		}
		sess.Flash("success", "Rule deleted; Nginx accepted the remaining rules.")
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	if text == "" {
		fail("Enter the rule.")
		return
	}
	newRule := security.ModsecRule{Text: text, Comment: comment}
	if target == nil {
		rules = append(rules, newRule)
	} else {
		rules[*target] = newRule
	}
	if _, err := s.modsecSaveCustomRules(ctx, r, security.ModsecJoinRules(rules), user); err != nil {
		modsecRuleFlashError(w, r, sess, to, err)
		return
	}
	on, ae := s.modsecCustomInUse(ctx)
	if ae != nil {
		sess.Flash("error", ae.Message)
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	note := ""
	if !on {
		note = modsecCustomNote
	}
	msg := "Rule added; Nginx accepted it."
	if target != nil {
		msg = "Rule saved; Nginx accepted it."
	}
	sess.Flash("success", msg+note)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// modsecRuleFlashError flashes a saveRule/saveCustom failure (the service's
// user-facing error, or the agent's message).
func modsecRuleFlashError(w http.ResponseWriter, r *http.Request, sess *session.Session, to string, err error) {
	me := &security.ModsecError{}
	if errors.As(err, &me) {
		sess.Flash("error", me.Message)
	} else {
		ae := agentCallError(err)
		sess.Flash("error", ae.Message)
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// modsecSaveCustom saves the whole custom rules file (saveCustom).
func (s *Server) modsecSaveCustom(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.modsecDenied(w)
		return
	}
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	to := "/security/modsec#custom"
	rules := strings.ReplaceAll(r.FormValue("rules"), "\r\n", "\n")
	n, err := s.modsecSaveCustomRules(ctx, r, rules, user)
	if err != nil {
		modsecRuleFlashError(w, r, sess, to, err)
		return
	}
	on, ae := s.modsecCustomInUse(ctx)
	if ae != nil {
		sess.Flash("error", ae.Message)
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	note := ""
	if !on {
		note = modsecCustomNote
	}
	sess.Flash("success", fmt.Sprintf("%d custom rule(s) saved; Nginx accepted them.", n)+note)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// modsecUpdate updates the rule packages in the background (update).
func (s *Server) modsecUpdate(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.modsecDenied(w)
		return
	}
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	to := "/security/modsec"
	res, err := s.agent.Call(ctx, "modsec.update", nil, 60*time.Second)
	if err != nil {
		agentErrorRedirect(w, r, "modsec update", err, to)
		return
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(res, &out)
	if out.JobID != "" {
		userID := user.ID
		_ = hosting.NewJobTracker(s.db).Track(ctx, s.currentServerID(r), out.JobID, "Update ModSecurity rules", "server", s.modsecServerID(r), &userID)
	}
	s.audit.Record(ctx, audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "modsec.update",
		TargetType: "server",
		TargetID:   s.modsecServerID(r),
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{"job": out.JobID},
	})
	sess.Flash("success", "Updating the rule packages in the background; you will be notified when it finishes.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

var modsecSHARe = regexp.MustCompile(`^[0-9a-f]{64}$`)
var modsecRollbackIDRe = regexp.MustCompile(`^[0-9TZ-]{17,40}[0-9a-f]*$`)

// modsecInstall installs (or upgrades) the Nginx connector (install).
func (s *Server) modsecInstall(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.modsecDenied(w)
		return
	}
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	to := "/security/modsec#installs"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
	method := "auto"
	if r.FormValue("method") == "source" {
		method = "source"
	}
	sha := strings.ToLower(strings.TrimSpace(r.FormValue("nginx_source_sha256")))
	if sha != "" && !modsecSHARe.MatchString(sha) {
		fail("The SHA-256 is 64 hexadecimal characters.")
		return
	}
	params := map[string]any{
		"method":         method,
		"upgrade":        r.FormValue("upgrade") == "1",
		"retention_days": s.modsecRetention(ctx),
	}
	if sha != "" {
		params["nginx_source_sha256"] = sha
	}
	res, err := s.agent.Call(ctx, "modsec.install", params, 60*time.Second)
	if err != nil {
		agentErrorRedirect(w, r, "modsec install", err, to)
		return
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(res, &out)
	kind := "Install the ModSecurity connector"
	if params["upgrade"] == true {
		kind = "Upgrade the ModSecurity connector"
	}
	if out.JobID != "" {
		userID := user.ID
		_ = hosting.NewJobTracker(s.db).Track(ctx, s.currentServerID(r), out.JobID, kind, "server", s.modsecServerID(r), &userID)
	}
	details := map[string]any{
		"job":            out.JobID,
		"method":         method,
		"upgrade":        params["upgrade"],
		"retention_days": params["retention_days"],
	}
	if sha != "" {
		details["nginx_source_sha256"] = sha
	}
	s.audit.Record(ctx, audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "modsec.install",
		TargetType: "server",
		TargetID:   s.modsecServerID(r),
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    details,
	})
	sess.Flash("success", "Installing the ModSecurity connector in the background. Everything is backed up first and rolled back if any step fails; you will be notified when it finishes.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// modsecInstallCrs installs the OWASP Core Rule Set (crs).
func (s *Server) modsecInstallCrs(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.modsecDenied(w)
		return
	}
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	to := "/security/modsec#installs"
	params := map[string]any{"retention_days": s.modsecRetention(ctx)}
	res, err := s.agent.Call(ctx, "modsec.crs_install", params, 60*time.Second)
	if err != nil {
		agentErrorRedirect(w, r, "modsec crs_install", err, to)
		return
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(res, &out)
	if out.JobID != "" {
		userID := user.ID
		_ = hosting.NewJobTracker(s.db).Track(ctx, s.currentServerID(r), out.JobID, "Install the OWASP Core Rule Set", "server", s.modsecServerID(r), &userID)
	}
	s.audit.Record(ctx, audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "modsec.crs_install",
		TargetType: "server",
		TargetID:   s.modsecServerID(r),
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{"job": out.JobID},
	})
	sess.Flash("success", "Installing the OWASP Core Rule Set in the background. It starts in detection only: websites set to block only log until you allow blocking.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// modsecRollback rolls one installation back (rollback/{id}).
func (s *Server) modsecRollback(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.modsecDenied(w)
		return
	}
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	to := "/security/modsec#installs"
	id := r.PathValue("id")
	if !modsecRollbackIDRe.MatchString(id) {
		sess.Flash("error", "Unknown installation.")
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	res, err := s.agent.Call(ctx, "modsec.rollback", map[string]any{"id": id}, 60*time.Second)
	if err != nil {
		agentErrorRedirect(w, r, "modsec rollback", err, to)
		return
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(res, &out)
	if out.JobID != "" {
		userID := user.ID
		_ = hosting.NewJobTracker(s.db).Track(ctx, s.currentServerID(r), out.JobID, "Roll back ModSecurity installation "+id, "server", s.modsecServerID(r), &userID)
	}
	s.audit.Record(ctx, audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "modsec.rollback",
		TargetType: "server",
		TargetID:   s.modsecServerID(r),
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{"job": out.JobID, "installation": id},
	})
	sess.Flash("success", "Rolling back installation "+id+" in the background. If Nginx would not accept the result, nothing changes.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// modsecSaveSite applies the administrator's per-website settings from the
// ModSecurity page (saveSite — the edit modal).
func (s *Server) modsecSaveSite(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.modsecDenied(w)
		return
	}
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	to := "/security/modsec#sites"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).Find(ctx, s.currentServerID(r), id, user)
	if err != nil || site == nil {
		fail("That website does not exist.")
		return
	}
	kept := map[string]bool{}
	for _, v := range r.Form["vendors[]"] {
		if v == "crs" || v == "custom" {
			kept[v] = true
		}
	}
	exclude := []string{}
	for _, v := range security.ModsecVendorIDs {
		if !kept[v] {
			exclude = append(exclude, v)
		}
	}
	disabled, err := security.ModsecParseIDs(r.FormValue("disabled_rules"))
	if err != nil {
		fail(err.Error())
		return
	}
	if err := s.modsecSetSite(ctx, r, site, r.FormValue("mode"), exclude, &disabled, true, user); err != nil {
		modsecRuleFlashError(w, r, sess, to, err)
		return
	}
	sess.Flash("success", "ModSecurity for "+site.Domain+" saved.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// ---- the website's Web firewall page ----

// siteFirewall renders a website's Web firewall page (site — websites.view).
func (s *Server) siteFirewall(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ctx := r.Context()
	sess := sessionFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.firewallNotFound(w)
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(ctx, s.currentServerID(r), id, user)
	if errors.Is(err, hosting.ErrWebsiteNotFound) || site == nil {
		s.firewallNotFound(w)
		return
	}
	if err != nil {
		s.log.Error("find website", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var status map[string]any
	var agentMsg string
	var events []map[string]any
	if st, ae := s.modsecStatus(ctx); ae != nil {
		agentMsg = ae.Message
	} else {
		status = st
		if ev, ae := s.modsecEvents(ctx, site, 50); ae != nil {
			agentMsg = ae.Message
		} else {
			events = ev
		}
	}
	isAdmin := false
	if hosting.IsAll(user) {
		if ok, _ := s.authorizer().Can(ctx, user, "security.manage"); ok {
			isAdmin = true
		}
	}
	canChange := false
	if ok, _ := s.authorizer().Can(ctx, user, "websites.manage"); ok {
		canChange = true
	}
	byDomain := map[string]int64{strings.ToLower(site.Domain): site.ID}
	for _, a := range site.Aliases {
		byDomain[strings.ToLower(a)] = site.ID
	}
	data := map[string]any{
		"User":         user,
		"CSRF":         session.CSRFToken(sess),
		"Site":         site,
		"AgentError":   agentMsg,
		"IsActive":     status != nil && status["active"] == true,
		"CanChange":    canChange,
		"CanManage":    isAdmin,
		"Events":       []modsecEventRow{},
		"Success":      "",
		"Error":        "",
		"ModeChecked":  map[string]bool{"off": true},
		"VendorsText":  "none",
		"DisabledText": "none",
	}
	if status != nil {
		set := security.ModsecSettingFor(status["sites"], site.Domain)
		data["ModeChecked"] = map[string]bool{set.Mode: true}
		kept := []string{}
		excluded := map[string]bool{}
		for _, e := range set.Exclude {
			excluded[e] = true
		}
		for _, v := range security.ModsecVendorIDs {
			if !excluded[v] {
				if v == "crs" {
					kept = append(kept, "OWASP Core Rule Set")
				} else {
					kept = append(kept, "Custom rules")
				}
			}
		}
		data["VendorsText"] = strings.Join(kept, ", ")
		if data["VendorsText"] == "" {
			data["VendorsText"] = "none"
		}
		if len(set.DisabledRules) > 0 {
			parts := make([]string, 0, len(set.DisabledRules))
			for _, d := range set.DisabledRules {
				parts = append(parts, strconv.Itoa(d))
			}
			data["DisabledText"] = strings.Join(parts, " ")
		}
		// "Turn off here" is an administrator action (PHP's
		// $disableFor = $isAdmin ? $site : null).
		turnOff := int64(0)
		if isAdmin {
			turnOff = site.ID
		}
		ev := modsecEventRows(events, byDomain, false, turnOff)
		data["Events"] = ev
		data["EventCtx"] = modsecEventsCtx{
			Events:        ev,
			TurnOffID:     turnOff,
			TurnOffDomain: site.Domain,
			CSRF:          session.CSRFToken(sess),
		}
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	s.render(w, "sites_firewall", data)
}

func (s *Server) firewallNotFound(w http.ResponseWriter) {
	renderStatus(w, s, "error", map[string]any{
		"Title":   "Website not found",
		"Message": "This website does not exist or you do not have access to it.",
	}, http.StatusNotFound)
}

// siteFirewallMode sets the website's mode (owners) or turns a rule off
// (administrators, from an event) — saveSiteMode, websites.manage.
func (s *Server) siteFirewallMode(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ctx := r.Context()
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	to := "/sites/" + r.PathValue("id") + "/firewall"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.firewallNotFound(w)
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).Find(ctx, s.currentServerID(r), id, user)
	if err != nil || site == nil {
		fail("That website does not exist.")
		return
	}
	isAdmin := false
	if hosting.IsAll(user) {
		if ok, _ := s.authorizer().Can(ctx, user, "security.manage"); ok {
			isAdmin = true
		}
	}
	mode := r.FormValue("mode")
	var disabled *[]int
	disableRule := r.FormValue("disable_rule")
	if isAdmin && disableRule != "" {
		status, ae := s.modsecStatus(ctx)
		if ae != nil {
			sess.Flash("error", ae.Message)
			http.Redirect(w, r, to, http.StatusSeeOther)
			return
		}
		cur := security.ModsecSettingFor(status["sites"], site.Domain)
		mode = cur.Mode
		added, perr := security.ModsecParseIDs(disableRule)
		if perr != nil {
			fail(perr.Error())
			return
		}
		merged := append([]int{}, cur.DisabledRules...)
		merged = append(merged, added...)
		disabled = &merged
	}
	if err := s.modsecSetSite(ctx, r, site, mode, nil, disabled, isAdmin, user); err != nil {
		modsecRuleFlashError(w, r, sess, to, err)
		return
	}
	msg := "Web firewall for " + site.Domain + ": " + strings.ToLower(security.ModsecModeLabel(mode)) + "."
	if disabled != nil {
		msg = "Rule " + disableRule + " is now off for " + site.Domain + "."
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// modsecSHAEquals is the rule's optimistic lock: hash_equals(sha256(text),
// the page's sha) — constant time.
func modsecSHAEquals(text, sha string) bool {
	sum := sha256.Sum256([]byte(text))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(sha)) == 1
}
