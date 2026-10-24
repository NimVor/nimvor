package http

// This file ports the cPHulk brute force protection page (HulkController:
// the index with the settings form, the countries management and the failed
// logins report, plus the save, countries, geo, unblock and clear actions).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/security"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// hulkDenied is the controller's deny(): the all-scope gate for the page,
// plus the role-admin gate for the changes.
func (s *Server) hulkDenied(w http.ResponseWriter) {
	renderStatus(w, s, "error", map[string]any{
		"Title":   "Access denied",
		"Message": "Only administrators manage brute force protection.",
	}, http.StatusForbidden)
}

// hulkService is the server's brute force protection service (set in New).
func (s *Server) hulkService() *security.Hulk { return s.hulk }

// hulkSync is the agent half of HulkService::sync.
func (s *Server) hulkSync(ctx context.Context, h *security.Hulk) error {
	p, err := h.BuildSync(ctx)
	if err != nil {
		return err
	}
	if !p.Enabled {
		_, err := s.agent.Call(ctx, "hulk.sync", map[string]any{"enabled": false}, 60*time.Second)
		return err
	}
	_, err = s.agent.Call(ctx, "hulk.sync", map[string]any{
		"enabled":       true,
		"blocks":        p.Blocks,
		"countries":     p.Countries,
		"whitelist":     p.Whitelist,
		"countries_all": p.CountriesAll,
	}, 120*time.Second)
	return err
}

// hulkSyncQuietly is HulkService::syncQuietly — the agent error is logged,
// the action still reports success.
func (s *Server) hulkSyncQuietly(ctx context.Context, h *security.Hulk, action string) {
	if err := s.hulkSync(ctx, h); err != nil {
		s.log.Error(action, "error", err)
	}
}

// hulkIndex renders the cPHulk page (HulkController::index,
// security.view, the all-scope gate).
func (s *Server) hulkIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.hulkDenied(w)
		return
	}
	ctx := r.Context()
	sess := sessionFrom(r.Context())
	h := s.hulkService()

	var st struct {
		Available bool `json:"available"`
		Active    bool `json:"active"`
	}
	agentMsg := ""
	var ae *agent.Error
	if res, err := s.agent.Call(ctx, "hulk.status", nil, 30*time.Second); err != nil {
		if errors.As(err, &ae) && ae.Message != "" {
			agentMsg = ae.Message
		} else {
			s.log.Error("hulk status", "error", err)
			agentMsg = "The WhaCP agent could not be reached."
		}
	} else if err := json.Unmarshal(res, &st); err != nil {
		s.log.Error("hulk status", "error", err)
		agentMsg = "The WhaCP agent answered with an unreadable state."
	}

	report, err := h.Report(ctx)
	if err != nil {
		s.log.Error("hulk report", "error", err)
	}
	blocks, err := h.Blocks(ctx)
	if err != nil {
		s.log.Error("hulk blocks", "error", err)
	}
	marks, err := h.Countries(ctx)
	if err != nil {
		s.log.Error("hulk countries", "error", err)
	}
	geoLoaded, _ := security.NewGeo(s.db).Loaded(ctx)

	ok, _ := s.authorizer().Can(ctx, user, "security.manage")
	canManage := ok && user.RoleName == "admin"

	v := hulkValues{
		Enabled:        hulkBool(s.settings, ctx, "hulk_enabled", false),
		UserEnabled:    hulkBool(s.settings, ctx, "hulk_user_enabled", true),
		UserPeriod:     hulkInt(s.settings, ctx, "hulk_user_period", 5),
		UserMax:        hulkInt(s.settings, ctx, "hulk_user_max", 5),
		UserScope:      hulkStr(s.settings, ctx, "hulk_user_scope", "all"),
		UserLockRoot:   hulkBool(s.settings, ctx, "hulk_user_lock_root", false),
		IPEnabled:      hulkBool(s.settings, ctx, "hulk_ip_enabled", true),
		IPPeriod:       hulkInt(s.settings, ctx, "hulk_ip_period", 15),
		IPMax:          hulkInt(s.settings, ctx, "hulk_ip_max", 15),
		IPCommand:      hulkStr(s.settings, ctx, "hulk_ip_command", ""),
		IPFirewall:     hulkBool(s.settings, ctx, "hulk_ip_firewall", false),
		OnedayMax:      hulkInt(s.settings, ctx, "hulk_oneday_max", 30),
		OnedayCommand:  hulkStr(s.settings, ctx, "hulk_oneday_command", ""),
		OnedayFirewall: hulkBool(s.settings, ctx, "hulk_oneday_firewall", false),
		Retention:      hulkInt(s.settings, ctx, "hulk_retention", 360),
		NotifyRoot:     hulkBool(s.settings, ctx, "hulk_notify_root", true),
		NotifyRootNB:   hulkBool(s.settings, ctx, "hulk_notify_root_netblock", false),
		NotifyBrute:    hulkBool(s.settings, ctx, "hulk_notify_brute", true),
		// The view's whitelist (implode("\n", the split entries), not the raw
		// setting text).
		Whitelist:    strings.Join(h.Whitelist(ctx), "\n"),
		GeoUpdated:   hulkStr(s.settings, ctx, "hulk_geo_updated", ""),
		CountriesAll: hulkBool(s.settings, ctx, "hulk_countries_all", false),
	}

	// The countries table, in the catalogue's order (the Go template cannot
	// range a map in a stable order).
	countries := make([]hulkCountryView, 0, len(security.Countries))
	for code, name := range security.Countries {
		countries = append(countries, hulkCountryView{Code: code, Name: name, Mark: marks[code]})
	}
	sort.Slice(countries, func(i, j int) bool { return countries[i].Code < countries[j].Code })

	reportView := make([]hulkReportView, 0, len(report))
	for _, r := range report {
		reportView = append(reportView, hulkReportView{
			User: r.User, IP: r.IP, CountryName: r.CountryName,
			Service: r.Service, AuthService: r.AuthService,
			Time: hulkTime(r.Time), Expires: hulkTime(r.Expires),
			MinutesLeft: r.MinutesLeft,
		})
	}
	blocksView := make([]hulkBlockView, 0, len(blocks))
	for _, b := range blocks {
		kind := "IP address"
		if b.Kind == "oneday" {
			kind = "IP address (one day)"
		} else if b.Kind == "user" {
			kind = "Account"
		}
		display := b.IP
		if display == "" {
			display = b.User
		}
		blocksView = append(blocksView, hulkBlockView{
			ID: b.ID, Display: display, Kind: kind, Firewall: b.Firewall,
			Reason: b.Reason, AuthService: b.AuthService, Failures: b.Failures,
			Expires: hulkTime(b.ExpiresAt)[:16],
		})
	}

	data := map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"V":          v,
		"Status":     nil,
		"AgentError": agentMsg,
		"CanManage":  canManage,
		"Whitelist":  v.Whitelist,
		"Countries":  countries,
		"GeoLoaded":  geoLoaded,
		"Report":     reportView,
		"Blocks":     blocksView,
		"Variables":  security.HulkVariables,
		"Pending":    s.securityPending(ctx),
	}
	if agentMsg == "" {
		data["Status"] = map[string]any{"Available": st.Available, "Active": st.Active}
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	s.render(w, "security_hulk", data)
}

// hulkCountryView is one row of the countries table (the catalogue order).
type hulkCountryView struct {
	Code string
	Name string
	Mark string // "" or "whitelist"/"blacklist" (the view's $countries)
}

// hulkReportView is one row of the failed-logins report (UTC times, the
// view's gmdate columns).
type hulkReportView struct {
	User        string
	IP          string
	CountryName string
	Service     string
	AuthService string
	Time        string
	Expires     string
	MinutesLeft int
}

// hulkBlockView is one active block (the view's blocks table).
type hulkBlockView struct {
	ID          int64
	Display     string // the IP address, or the username for account blocks
	Kind        string
	Firewall    bool
	Reason      string
	AuthService string
	Failures    int
	Expires     string // "2006-01-02 15:04" UTC
}

// hulkTime is gmdate (UTC).
func hulkTime(t int64) string {
	return time.Unix(t, 0).UTC().Format("2006-01-02 15:04:05")
}

// hulkValues is the settings form's values (the view's $values).
type hulkValues struct {
	Enabled        bool
	UserEnabled    bool
	UserPeriod     int
	UserMax        int
	UserScope      string
	UserLockRoot   bool
	IPEnabled      bool
	IPPeriod       int
	IPMax          int
	IPCommand      string
	IPFirewall     bool
	OnedayMax      int
	OnedayCommand  string
	OnedayFirewall bool
	Retention      int
	NotifyRoot     bool
	NotifyRootNB   bool
	NotifyBrute    bool
	Whitelist      string
	GeoUpdated     string
	CountriesAll   bool
}

// hulkBool / hulkInt / hulkStr are the settings accessors with their default
// (SettingsService's definition table defaults, the view's $values fallback).
func hulkBool(rep *settings.Repository, ctx context.Context, key string, def bool) bool {
	v, _ := rep.Bool(ctx, key, def)
	return v
}

func hulkInt(rep *settings.Repository, ctx context.Context, key string, def int) int {
	v, _ := rep.Int(ctx, key, def)
	return v
}

func hulkStr(rep *settings.Repository, ctx context.Context, key string, def string) string {
	v, _ := rep.String(ctx, key, def)
	return v
}

// settingsRaw is the stored JSON text of a setting ("" when absent).
func (s *Server) settingsRaw(ctx context.Context, key string) (string, bool) {
	var v sql.NullString
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v); err != nil {
		return "", false
	}
	return v.String, true
}

// hulkDefaults is the definition table's defaults (SettingsService::all
// falls back to them, and update() compares against them).
var hulkDefaults = map[string]any{
	"hulk_enabled":              false,
	"hulk_user_enabled":         true,
	"hulk_user_period":          5,
	"hulk_user_max":             5,
	"hulk_user_scope":           "all",
	"hulk_user_lock_root":       false,
	"hulk_ip_enabled":           true,
	"hulk_ip_period":            15,
	"hulk_ip_max":               15,
	"hulk_ip_command":           "",
	"hulk_ip_firewall":          false,
	"hulk_oneday_max":           30,
	"hulk_oneday_command":       "",
	"hulk_oneday_firewall":      false,
	"hulk_retention":            360,
	"hulk_notify_root":          true,
	"hulk_notify_root_netblock": false,
	"hulk_notify_brute":         true,
	"hulk_whitelist":            "",
}

// hulkBefore is SettingsService::all's value for the key (the default when
// the setting is absent), decoded as the type of sample.
func (s *Server) hulkBefore(ctx context.Context, key string, sample any) any {
	raw, ok := s.settingsRaw(ctx, key)
	def := hulkDefaults[key]
	if !ok {
		return def
	}
	switch sample.(type) {
	case bool:
		var v bool
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return def
		}
		return v
	case int:
		var v int
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return def
		}
		return v
	default:
		var v string
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return def
		}
		return v
	}
}

// hulkSave applies the settings form (HulkController::save — security.manage
// plus the role-admin gate).
func (s *Server) hulkSave(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !hosting.IsAll(user) || user.RoleName != "admin" {
		s.hulkDenied(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	to := "/security/hulk"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}

	scope := r.FormValue("hulk_user_scope")
	if scope != "local" && scope != "all" {
		fail("Choose a protection type.")
		return
	}
	entries := []string{}
	for _, p := range strings.FieldsFunc(r.FormValue("hulk_whitelist"), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ','
	}) {
		if p != "" {
			entries = append(entries, p)
		}
	}
	for _, e := range entries {
		if !security.ValidEntry(e) {
			fail(fmt.Sprintf("%q is not an IP address or network.", strimwidth(e, 60)))
			return
		}
	}
	if len(entries) > 200 {
		fail("At most 200 whitelisted addresses.")
		return
	}
	// Turning protection on with an empty whitelist: this administrator's
	// address is added, so they cannot lock themselves out.
	note := ""
	wasEnabled, _ := s.settings.Bool(ctx, "hulk_enabled", false)
	if len(entries) == 0 && formBool(r.FormValue("hulk_enabled")) && !wasEnabled {
		ip := clientIP(r)
		entries = append(entries, ip)
		note = fmt.Sprintf(" Your address (%s) was added to the whitelist.", ip)
	}
	whitelist := strings.Join(entries, ", ")
	ipCommand := strings.TrimSpace(r.FormValue("hulk_ip_command"))
	onedayCommand := strings.TrimSpace(r.FormValue("hulk_oneday_command"))

	// SettingsService::update (only the keys the form sends).
	userPeriod, e1 := policyInt(r.FormValue("hulk_user_period"), 1, 1440)
	userMax, e2 := policyInt(r.FormValue("hulk_user_max"), 1, 1000)
	ipPeriod, e3 := policyInt(r.FormValue("hulk_ip_period"), 1, 1440)
	ipMax, e4 := policyInt(r.FormValue("hulk_ip_max"), 1, 1000)
	onedayMax, e5 := policyInt(r.FormValue("hulk_oneday_max"), 1, 10000)
	retention, e6 := policyInt(r.FormValue("hulk_retention"), 5, 10080)
	e7 := hulkStringError(ipCommand, 500)
	e8 := hulkStringError(onedayCommand, 500)
	e9 := hulkStringError(whitelist, 4000)
	// The SettingsService::update order (definition table): user period/max,
	// ip period/max/command, oneday max/command, retention, whitelist.
	if msgs := strings.Join([]string{e1, e2, e3, e4, e7, e5, e8, e6, e9}, " "); msgs != "" {
		fail(msgs)
		return
	}
	changed := map[string]any{
		"hulk_enabled":              formBool(r.FormValue("hulk_enabled")),
		"hulk_user_enabled":         formBool(r.FormValue("hulk_user_enabled")),
		"hulk_user_period":          userPeriod,
		"hulk_user_max":             userMax,
		"hulk_user_scope":           scope,
		"hulk_user_lock_root":       formBool(r.FormValue("hulk_user_lock_root")),
		"hulk_ip_enabled":           formBool(r.FormValue("hulk_ip_enabled")),
		"hulk_ip_period":            ipPeriod,
		"hulk_ip_max":               ipMax,
		"hulk_ip_command":           ipCommand,
		"hulk_ip_firewall":          formBool(r.FormValue("hulk_ip_firewall")),
		"hulk_oneday_max":           onedayMax,
		"hulk_oneday_command":       onedayCommand,
		"hulk_oneday_firewall":      formBool(r.FormValue("hulk_oneday_firewall")),
		"hulk_retention":            retention,
		"hulk_notify_root":          formBool(r.FormValue("hulk_notify_root")),
		"hulk_notify_root_netblock": formBool(r.FormValue("hulk_notify_root_netblock")),
		"hulk_notify_brute":         formBool(r.FormValue("hulk_notify_brute")),
		"hulk_whitelist":            whitelist,
	}
	diff := map[string]any{}
	for k, v := range changed {
		if before := s.hulkBefore(ctx, k, v); before != v {
			if err := s.settings.Set(ctx, k, v, user.ID); err != nil {
				s.log.Error("save hulk settings", "key", k, "error", err)
				fail("The settings could not be saved.")
				return
			}
			diff[k] = map[string]any{"from": before, "to": v}
		}
	}
	if len(diff) > 0 {
		s.audit.Record(ctx, audit.Entry{
			UserID:     &user.ID,
			Username:   user.Username,
			Action:     "settings.update",
			TargetType: "settings",
			TargetID:   "panel",
			Result:     audit.Success,
			IP:         clientIP(r),
			UserAgent:  r.UserAgent(),
			Details:    map[string]any{"changed": diff},
		})
	}
	h := s.hulkService()
	if err := s.hulkSync(ctx, h); err != nil {
		agentErrorRedirect(w, r, "hulk sync", err, to)
		return
	}
	sess.Flash("success", "cPHulk settings saved."+note)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// hulkStringError is SettingsService's printable-string check for the
// optional command and whitelist fields.
func hulkStringError(v string, max int) string {
	if v == "" {
		return ""
	}
	if len(v) > max || hasControlChars(v) {
		return fmt.Sprintf("Enter between 1 and %d printable characters.", max)
	}
	return ""
}

func hasControlChars(v string) bool {
	for _, r := range v {
		if r < 0x20 || r == 0x7F {
			return true
		}
	}
	return false
}

// hulkCountries applies the countries marks (HulkController::countries —
// security.manage plus the role-admin gate).
func (s *Server) hulkCountries(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !hosting.IsAll(user) || user.RoleName != "admin" {
		s.hulkDenied(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	to := "/security/hulk#countries"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}

	bulk := r.FormValue("bulk")
	marks := map[string]string{}
	if bulk != "" {
		if bulk != "whitelist" && bulk != "blacklist" && bulk != "none" {
			fail("Choose how to mark the countries.")
			return
		}
		for _, code := range r.Form["selected[]"] {
			marks[code] = bulk
		}
	} else {
		for key, values := range r.Form {
			if len(values) == 0 || !strings.HasPrefix(key, "country[") || !strings.HasSuffix(key, "]") {
				continue
			}
			marks[key[len("country["):len(key)-1]] = values[0]
		}
	}
	scope := r.FormValue("scope")
	if scope != "" {
		if scope != "login" && scope != "all" {
			fail("Choose where blacklisted countries are blocked.")
			return
		}
		all := scope == "all"
		if cur, _ := s.settings.Bool(ctx, "hulk_countries_all", false); cur != all {
			if err := s.settings.Set(ctx, "hulk_countries_all", all, user.ID); err != nil {
				s.log.Error("save hulk countries scope", "error", err)
				fail("The settings could not be saved.")
				return
			}
		}
	}
	h := s.hulkService()
	n, err := h.SetCountries(ctx, marks)
	if err != nil {
		s.log.Error("hulk set countries", "error", err)
		fail("The country marks could not be saved.")
		return
	}
	s.audit.Record(ctx, audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "hulk.countries",
		TargetType: "server",
		TargetID:   "hulk",
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{"marked": marks},
	})
	if err := s.hulkSync(ctx, h); err != nil {
		agentErrorRedirect(w, r, "hulk sync", err, to)
		return
	}
	plural := "ies"
	if n == 1 {
		plural = "y"
	}
	sess.Flash("success", fmt.Sprintf("%d countr%s updated.", n, plural))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// hulkGeo imports the DB-IP country database (HulkController::geoUpload —
// security.manage plus the role-admin gate).
func (s *Server) hulkGeo(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !hosting.IsAll(user) || user.RoleName != "admin" {
		s.hulkDenied(w)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	to := "/security/hulk#countries"
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
	file, _, err := r.FormFile("database")
	if err != nil {
		fail(`Choose the DB-IP "IP to Country Lite" CSV file (.csv or .csv.gz).`)
		return
	}
	defer file.Close()
	// GeoIpService::import reads at most MAX_BYTES+1 from the file.
	raw, err := io.ReadAll(io.LimitReader(file, security.GeoMaxBytes+1))
	if err != nil {
		fail("The file could not be read.")
		return
	}
	geo := security.NewGeo(s.db)
	n, err := geo.Import(ctx, raw)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	// The PHP records the stamp with a null actor (settings->update, null),
	// with its own settings.update audit row when the value changed.
	stamp := security.ImportStamp()
	if old := hulkStr(s.settings, ctx, "hulk_geo_updated", ""); old != stamp {
		if err := s.settings.Set(ctx, "hulk_geo_updated", stamp, 0); err != nil {
			s.log.Error("save hulk geo stamp", "error", err)
		} else {
			s.audit.Record(ctx, audit.Entry{
				UserID:     &user.ID,
				Username:   user.Username,
				Action:     "settings.update",
				TargetType: "settings",
				TargetID:   "panel",
				Result:     audit.Success,
				IP:         clientIP(r),
				UserAgent:  r.UserAgent(),
				Details: map[string]any{"changed": map[string]any{
					"hulk_geo_updated": map[string]any{"from": old, "to": stamp},
				}},
			})
		}
	}
	s.audit.Record(ctx, audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "hulk.geo_import",
		TargetType: "server",
		TargetID:   "geoip",
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{"ranges": n},
	})
	sess.Flash("success", fmt.Sprintf("Country database loaded (%d address ranges).", n))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// hulkUnblock removes one block (HulkController::unblock — security.manage
// plus the role-admin gate).
func (s *Server) hulkUnblock(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !hosting.IsAll(user) || user.RoleName != "admin" {
		s.hulkDenied(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		id = 0
	}
	h := s.hulkService()
	if err := h.RemoveBlock(ctx, id); err != nil {
		s.log.Error("hulk remove block", "error", err)
		sess.Flash("error", "The block could not be removed.")
		http.Redirect(w, r, "/security/hulk#report", http.StatusSeeOther)
		return
	}
	s.audit.Record(ctx, audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "hulk.unblock",
		TargetType: "server",
		TargetID:   "hulk",
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{"id": id},
	})
	s.hulkSyncQuietly(ctx, h, "hulk sync after unblock")
	sess.Flash("success", "The block was removed.")
	http.Redirect(w, r, "/security/hulk#report", http.StatusSeeOther)
}

// hulkClear removes every block and clears the report (HulkController::clear
// — security.manage plus the role-admin gate).
func (s *Server) hulkClear(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if !hosting.IsAll(user) || user.RoleName != "admin" {
		s.hulkDenied(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	h := s.hulkService()
	if err := h.Clear(ctx); err != nil {
		s.log.Error("hulk clear", "error", err)
		sess.Flash("error", "The report could not be cleared.")
		http.Redirect(w, r, "/security/hulk#report", http.StatusSeeOther)
		return
	}
	s.audit.Record(ctx, audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "hulk.clear",
		TargetType: "server",
		TargetID:   "hulk",
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{},
	})
	s.hulkSyncQuietly(ctx, h, "hulk sync after clear")
	sess.Flash("success", "All blocks were removed and the report was cleared.")
	http.Redirect(w, r, "/security/hulk#report", http.StatusSeeOther)
}
