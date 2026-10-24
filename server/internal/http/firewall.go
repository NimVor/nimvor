package http

// This file ports the firewall page (SecurityController, Security →
// Firewall): the UFW state (status bar, the ports WhaCP keeps open, the
// rules with their listening state, the enable form), the guarded change
// actions (turn the firewall on/off, add/delete a rule, the CSF-style
// flood and connection-limit protections) and their confirm flow via the
// unconfirmed-change banner (security.confirm/undo, ported with the shell
// page).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// publicAddress is SecurityService::publicAddress — an address worth
// allowing (a real IP that is not loopback; private ranges are fine, the
// agent opens the port for it).
func publicAddress(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && !strings.HasPrefix(ip, "127.") && ip != "::1"
}

// guardedResult is the agent's guarded-change reply (agent/internal/
// security's guarded(): a mutation that is undone unless confirmed).
type guardedResult struct {
	Changed bool `json:"changed"`
	Pending struct {
		ID string `json:"id"`
	} `json:"pending"`
}

// guardRecord is SecurityService::recordGuarded: the audit row (server /
// security, the details plus the pending change's id) and the flash
// message.
func (s *Server) guardRecord(r *http.Request, action string, reply json.RawMessage, details map[string]any) string {
	user := userFrom(r.Context())
	var res guardedResult
	if err := json.Unmarshal(reply, &res); err != nil {
		s.log.Error(action, "reply", err)
	}
	if details == nil {
		details = map[string]any{}
	}
	details["pending"] = res.Pending.ID
	s.audit.Record(r.Context(), audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     action,
		TargetType: "server",
		TargetID:   "security",
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    details,
	})
	if !res.Changed {
		return "Nothing to change."
	}
	return "Applied. Check that you can still connect, then confirm within 2 minutes — otherwise the change is undone automatically."
}

// --------------------------------------------------------------------------
// GET /security/firewall (security.firewall, security.view)
// --------------------------------------------------------------------------

// firewallRuleRow is one UFW rule as the table shows it (the view's
// listening-state and label lookups precomputed — templates can't).
type firewallRuleRow struct {
	Spec         string
	Proto        string // "tcp/udp" when the rule has no protocol
	Port         string // ", "-separated; "" = all
	PortEmpty    bool
	SSH          bool
	State        string // listening, local, idle or ""
	ActionLabel  string
	ActionClass  string
	DirectionLbl string
	From         string
	Comment      string
	Kind         string // data-dt-kind: port or ip
	DirKey       string // data-dt-dir: in, out or "in out" (route)
}

// firewallRequiredRow is one "ports WhaCP keeps open" row.
type firewallRequiredRow struct {
	Port        string
	Proto       string // display (upper)
	ProtoRaw    string // the agent's value, what the Allow form sends
	From        string
	Service     string
	Critical    bool
	Open        bool
	StatusLabel string
	StatusClass string
	Comment     string // the cleaned service name the Allow form sends
}

// firewallProtectionView is the protection form's values (the applied
// configuration from the agent, the fresh-install defaults when none).
type firewallProtectionView struct {
	Available      bool
	Active         bool
	Enabled        bool
	InvalidChecked bool // checked when fresh or applied
	SynRate        string
	ICMPRate       string
	ConnLimits     string
	PortFlood      string
	Exempt         string
}

type firewallDropped struct {
	Label string
	Count string // comma-formatted
}

type portLimit struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"`
	Max   int    `json:"max"`
}

// firewallIndex renders the firewall page (SecurityController::firewall).
func (s *Server) firewallIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	myIP := ""
	if ip := clientIP(r); publicAddress(ip) {
		myIP = ip
	}

	var st struct {
		Backend         string `json:"backend"`
		Installed       bool   `json:"installed"`
		Active          bool   `json:"active"`
		DefaultIncoming string `json:"default_incoming"`
		DefaultOutgoing string `json:"default_outgoing"`
		SSHPort         int    `json:"ssh_port"`
		SSHAllowed      bool   `json:"ssh_allowed"`
		Rules           []struct {
			Spec      string `json:"spec"`
			Action    string `json:"action"`
			SSH       bool   `json:"ssh"`
			Port      string `json:"port"`
			Proto     string `json:"proto"`
			From      string `json:"from"`
			Direction string `json:"direction"`
			Comment   string `json:"comment"`
		} `json:"rules"`
		Required []struct {
			Port     string `json:"port"`
			Proto    string `json:"proto"`
			From     string `json:"from"`
			Service  string `json:"service"`
			Critical bool   `json:"critical"`
			Open     bool   `json:"open"`
		} `json:"required"`
		Listening []struct {
			Proto string `json:"proto"`
			Port  int    `json:"port"`
			Local bool   `json:"local"`
		} `json:"listening"`
	}
	agentMsg := ""
	var ae *agent.Error
	res, err := s.agent.Call(r.Context(), "firewall.status", nil)
	if err != nil {
		if errors.As(err, &ae) && ae.Message != "" {
			agentMsg = ae.Message
		} else {
			s.log.Error("firewall status", "error", err)
			agentMsg = "The WhaCP agent could not be reached."
		}
	} else if err := json.Unmarshal(res, &st); err != nil {
		s.log.Error("firewall status", "error", err)
		agentMsg = "The WhaCP agent answered with an unreadable state."
	}

	data := map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"MyIP":       myIP,
		"Fw":         nil,
		"Rules":      nil,
		"Required":   nil,
		"Protection": nil,
		"Dropped":    nil,
		"PortRules":  0,
		"IPRules":    0,
		"AgentError": agentMsg,
	}
	if agentMsg == "" {
		// proto/port → reachable from outside (a loopback-only listener
		// marks the port as listening-local).
		listening := map[string]bool{}
		for _, l := range st.Listening {
			key := l.Proto + "/" + strconv.Itoa(l.Port)
			listening[key] = listening[key] || !l.Local
		}
		rules := make([]firewallRuleRow, 0, len(st.Rules))
		portRules, ipRules := 0, 0
		for _, ru := range st.Rules {
			row := firewallRuleRow{
				Spec: ru.Spec, SSH: ru.SSH, Port: strings.ReplaceAll(ru.Port, ",", ", "),
				PortEmpty: ru.Port == "", From: ru.From, Comment: ru.Comment,
			}
			if ru.Proto == "" {
				row.Proto = "tcp/udp"
			} else {
				row.Proto = ru.Proto
			}
			switch ru.Direction {
			case "out":
				row.DirectionLbl, row.DirKey = "Outbound", "out"
			case "route":
				row.DirectionLbl, row.DirKey = "Routed", "in out"
			default:
				row.DirectionLbl, row.DirKey = "Inbound", "in"
			}
			switch ru.Action {
			case "allow":
				row.ActionLabel, row.ActionClass = "Allow", "wcp-good"
			case "limit":
				row.ActionLabel, row.ActionClass = "Allow (rate-limited)", "wcp-good"
			case "deny":
				row.ActionLabel, row.ActionClass = "Deny", "wcp-bad"
			case "reject":
				row.ActionLabel, row.ActionClass = "Reject", "wcp-bad"
			default:
				if ru.Action != "" {
					row.ActionLabel = strings.ToUpper(ru.Action[:1]) + ru.Action[1:]
				}
			}
			row.State = ruleState(ru.Port, ru.Proto, listening)
			if ru.Port == "" {
				row.Kind = "ip"
				ipRules++
			} else {
				row.Kind = "port"
				portRules++
			}
			rules = append(rules, row)
		}
		required := make([]firewallRequiredRow, 0, len(st.Required))
		for _, p := range st.Required {
			row := firewallRequiredRow{
				Port: p.Port, Proto: strings.ToUpper(p.Proto), ProtoRaw: p.Proto, From: p.From,
				Service: p.Service, Critical: p.Critical, Open: p.Open,
				Comment: cleanServiceComment(p.Service),
			}
			switch {
			case p.Open:
				row.StatusLabel, row.StatusClass = "Allowed", "is-ok"
			case st.Active:
				row.StatusLabel, row.StatusClass = "Not allowed", "is-warn"
			default:
				row.StatusLabel, row.StatusClass = "Opened when turned on", "is-warn"
			}
			required = append(required, row)
		}
		// The protections (an agent without the flood support: no panel
		// for them — the PHP page's silent catch).
		var prot *firewallProtectionView
		var dropped []firewallDropped
		if pres, perr := s.agent.Call(r.Context(), "firewall.protection", nil); perr == nil {
			var pv struct {
				Available bool `json:"available"`
				Active    bool `json:"active"`
				Config    struct {
					Enabled    bool        `json:"enabled"`
					Invalid    bool        `json:"invalid"`
					SynRate    int         `json:"syn_rate"`
					ICMPRate   int         `json:"icmp_rate"`
					ConnLimits []portLimit `json:"conn_limits"`
					PortFlood  []portLimit `json:"port_flood"`
					Exempt     []string    `json:"exempt"`
				} `json:"config"`
				Dropped map[string]int64 `json:"dropped"`
			}
			if json.Unmarshal(pres, &pv) == nil {
				fresh := !pv.Config.Enabled && !pv.Config.Invalid && pv.Config.SynRate == 0 &&
					len(pv.Config.ConnLimits) == 0 && len(pv.Config.PortFlood) == 0 && pv.Config.ICMPRate == 0
				prot = &firewallProtectionView{
					Available:      pv.Available,
					Active:         pv.Active,
					Enabled:        pv.Config.Enabled,
					InvalidChecked: fresh || pv.Config.Invalid,
					SynRate:        strconv.Itoa(pv.Config.SynRate),
					ICMPRate:       strconv.Itoa(pv.Config.ICMPRate),
					ConnLimits:     limitString(pv.Config.ConnLimits, false),
					PortFlood:      limitString(pv.Config.PortFlood, true),
					Exempt:         strings.Join(pv.Config.Exempt, ", "),
				}
				if fresh {
					prot.SynRate, prot.ICMPRate = "100", "5"
				}
				dropped = droppedItems(pv.Dropped)
			}
		}
		data["Fw"] = &st
		data["Rules"] = rules
		data["Required"] = required
		data["Protection"] = prot
		data["Dropped"] = dropped
		data["PortRules"] = portRules
		data["IPRules"] = ipRules
	}
	ok, _ := s.authorizer().Can(r.Context(), user, "security.manage")
	data["CanManage"] = ok
	data["Pending"] = s.securityPending(r.Context())
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	s.render(w, "security_firewall", data)
}

// namedRulePorts are the well-known service names UFW rules may carry
// (the view's $named).
var namedRulePorts = map[string][]int{
	"openssh": {22}, "ssh": {22}, "http": {80}, "https": {443},
	"nginx full": {80, 443}, "nginx http": {80}, "nginx https": {443},
	"smtp": {25}, "submission": {587}, "smtps": {465}, "imap": {143},
	"imaps": {993}, "pop3": {110}, "pop3s": {995}, "dns": {53}, "ftp": {21}, "mysql": {3306},
}

var rulePortRangeRe = regexp.MustCompile(`^(\d+)[:-](\d+)$`)

// rulePorts is the view's $ports closure: the ports of a rule's port field
// ("" or an unparseable entry → nil, i.e. not a port rule).
func rulePorts(p string) []int {
	if p == "" {
		return nil
	}
	if ns, ok := namedRulePorts[strings.ToLower(p)]; ok {
		return ns
	}
	var out []int
	for _, part := range strings.Split(p, ",") {
		if m := rulePortRangeRe.FindStringSubmatch(part); m != nil {
			lo, _ := strconv.Atoi(m[1])
			hi, _ := strconv.Atoi(m[2])
			if hi-lo > 1000 {
				return nil
			}
			for i := lo; i <= hi; i++ {
				out = append(out, i)
			}
		} else if isDigits(part) {
			n, _ := strconv.Atoi(part)
			out = append(out, n)
		} else {
			return nil
		}
	}
	return out
}

// ruleState is the view's $state closure: listening (reachable), local
// (loopback only) or idle, "" when the rule is not a port rule.
func ruleState(port, proto string, listening map[string]bool) string {
	ports := rulePorts(port)
	if ports == nil {
		return ""
	}
	protos := []string{"tcp", "udp"}
	if proto != "" {
		protos = []string{proto}
	}
	local := false
	for _, p := range ports {
		for _, pr := range protos {
			key := pr + "/" + strconv.Itoa(p)
			if v, ok := listening[key]; ok {
				if v {
					return "listening"
				}
				local = true
			}
		}
	}
	if local {
		return "local"
	}
	return "idle"
}

// commentCleanRe reduces the service names to the view's allowed comment
// characters.
var commentCleanRe = regexp.MustCompile(`[^A-Za-z0-9 ._:/-]+`)

// cleanServiceComment is the view's Allow-button comment: the service name
// reduced, spaces collapsed, 60 bytes.
func cleanServiceComment(service string) string {
	cleaned := commentCleanRe.ReplaceAllString(service, " ")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if len(cleaned) > 60 {
		cleaned = cleaned[:60]
	}
	return cleaned
}

// limitString renders the limits for the form ("22:10, 80:150" or
// "tcp/22:20, udp/53:600").
func limitString(limits []portLimit, withProto bool) string {
	parts := make([]string, 0, len(limits))
	for _, l := range limits {
		if withProto {
			parts = append(parts, l.Proto+"/"+strconv.Itoa(l.Port)+":"+strconv.Itoa(l.Max))
		} else {
			parts = append(parts, strconv.Itoa(l.Port)+":"+strconv.Itoa(l.Max))
		}
	}
	return strings.Join(parts, ", ")
}

// droppedItems is the protection panel's dropped counters (the key labels,
// comma-formatted counts, sorted — the agent sends a JSON object).
func droppedItems(counts map[string]int64) []firewallDropped {
	names := map[string]string{"invalid": "invalid packets", "syn": "SYN flood", "icmp": "ping flood"}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]firewallDropped, 0, len(keys))
	for _, k := range keys {
		label := names[k]
		if label == "" {
			switch {
			case strings.HasPrefix(k, "conn:"):
				label = "connections to port " + k[len("conn:"):]
			case strings.HasPrefix(k, "rate:"):
				label = "flood on " + k[len("rate:"):]
			default:
				label = k
			}
		}
		out = append(out, firewallDropped{Label: label, Count: commaFormat(counts[k])})
	}
	return out
}

// commaFormat is number_format for integers.
func commaFormat(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	rest := len(s) % 3
	if rest > 0 {
		b.WriteString(s[:rest])
	}
	for i := rest; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// --------------------------------------------------------------------------
// The guarded changes (all security.manage, CSRF, flash + 303)
// --------------------------------------------------------------------------

// firewallToggle turns the firewall on or off (SecurityController::
// toggleFirewall → SecurityService::setFirewall).
func (s *Server) firewallToggle(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	to := "/security/firewall"
	if r.FormValue("enabled") == "1" {
		mine := ""
		if r.FormValue("allow_me") == "1" {
			mine = clientIP(r)
		}
		allowFrom := ""
		if publicAddress(mine) {
			allowFrom = mine
		}
		// PHP: allow_services defaults to '1' (an unchecked switch sends 0).
		allowServices := r.FormValue("allow_services") == "" || r.FormValue("allow_services") == "1"
		reply, err := s.agent.Call(r.Context(), "firewall.set_enabled", map[string]any{
			"enabled": true, "allow_services": allowServices, "allow_from": allowFrom,
		}, 90*time.Second)
		if err != nil {
			agentErrorRedirect(w, r, "firewall toggle", err, to)
			return
		}
		sess.Flash("success", s.guardRecord(r, "firewall.enable", reply, map[string]any{
			"enabled": true, "allow_services": allowServices, "allow_from": allowFrom,
		}))
		// PHP also whitelists the address in cPHulk — the brute-force
		// protection service is not ported yet, so this is skipped.
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	reply, err := s.agent.Call(r.Context(), "firewall.set_enabled", map[string]any{"enabled": false})
	if err != nil {
		agentErrorRedirect(w, r, "firewall toggle", err, to)
		return
	}
	sess.Flash("success", s.guardRecord(r, "firewall.disable", reply, nil))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// firewallAddRule adds a UFW rule (SecurityController::addRule).
func (s *Server) firewallAddRule(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	action := r.FormValue("action")
	if action == "" {
		action = "allow"
	}
	rule := map[string]any{
		"action":  action,
		"port":    strings.TrimSpace(r.FormValue("port")),
		"proto":   r.FormValue("proto"),
		"from":    strings.TrimSpace(r.FormValue("from")),
		"comment": strings.TrimSpace(r.FormValue("comment")),
	}
	reply, err := s.agent.Call(r.Context(), "firewall.rule_add", rule)
	if err != nil {
		agentErrorRedirect(w, r, "firewall rule", err, "/security/firewall")
		return
	}
	sess.Flash("success", s.guardRecord(r, "firewall.rule_add", reply, rule))
	http.Redirect(w, r, "/security/firewall", http.StatusSeeOther)
}

// firewallDeleteRule deletes a UFW rule by spec (SecurityController::
// deleteRule).
func (s *Server) firewallDeleteRule(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	spec := r.FormValue("spec")
	reply, err := s.agent.Call(r.Context(), "firewall.rule_delete", map[string]any{"spec": spec})
	if err != nil {
		agentErrorRedirect(w, r, "firewall rule", err, "/security/firewall")
		return
	}
	sess.Flash("success", s.guardRecord(r, "firewall.rule_delete", reply, map[string]any{"rule": spec}))
	http.Redirect(w, r, "/security/firewall", http.StatusSeeOther)
}

var (
	numberRe       = regexp.MustCompile(`^\d{1,7}$`)
	limitNoProtoRe = regexp.MustCompile(`^(\d{1,5}):(\d{1,6})$`)
	limitProtoRe   = regexp.MustCompile(`^(?:(tcp|udp))?/(\d{1,5}):(\d{1,7})$`)
)

// protectionNum is setProtection's $num: an optional number 0..max.
func protectionNum(v, what string, max int) (int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	if !numberRe.MatchString(v) {
		return 0, fmt.Errorf("%s: a number from 0 to %d.", what, max)
	}
	n, _ := strconv.Atoi(v)
	if n > max {
		return 0, fmt.Errorf("%s: a number from 0 to %d.", what, max)
	}
	return n, nil
}

// protectionPortLimits is SecurityService::portLimits: "22:10, 80:150"
// (connection limits, TCP) or "tcp/80:300, udp/53:600" (port flood).
func protectionPortLimits(text string, withProto bool, what string) ([]portLimit, error) {
	var out []portLimit
	for _, item := range strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == ';'
	}) {
		if item == "" {
			continue
		}
		if withProto {
			m := limitProtoRe.FindStringSubmatch(strings.ToLower(item))
			if m == nil || limitRangeCheck(m[2], m[3]) != nil {
				return nil, fmt.Errorf("%s: %q is not like tcp/80:300.", what, clip40(item))
			}
			proto := m[1]
			if proto == "" {
				proto = "tcp"
			}
			port, _ := strconv.Atoi(m[2])
			max, _ := strconv.Atoi(m[3])
			out = append(out, portLimit{Port: port, Proto: proto, Max: max})
		} else {
			m := limitNoProtoRe.FindStringSubmatch(item)
			if m == nil || limitRangeCheck(m[1], m[2]) != nil {
				return nil, fmt.Errorf("%s: %q is not like 22:10.", what, clip40(item))
			}
			port, _ := strconv.Atoi(m[1])
			max, _ := strconv.Atoi(m[2])
			out = append(out, portLimit{Port: port, Proto: "tcp", Max: max})
		}
	}
	if len(out) > 50 {
		return nil, fmt.Errorf("%s: at most 50 ports.", what)
	}
	return out, nil
}

// limitRangeCheck is the regex's numeric clauses (port 1..65535, max ≥ 1).
func limitRangeCheck(portS, maxS string) error {
	port, _ := strconv.Atoi(portS)
	max, _ := strconv.Atoi(maxS)
	if port < 1 || port > 65535 || max < 1 {
		return errors.New("range")
	}
	return nil
}

func clip40(s string) string {
	if len(s) > 40 {
		return s[:40]
	}
	return s
}

func clip60(s string) string {
	if len(s) > 60 {
		return s[:60]
	}
	return s
}

// validNetEntry is HulkService::validEntry: an IP address, optionally with
// a prefix length not wider than the address.
func validNetEntry(s string) bool {
	addr, lenStr := s, ""
	if i := strings.IndexByte(s, '/'); i >= 0 {
		addr, lenStr = s[:i], s[i+1:]
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return false
	}
	if lenStr == "" {
		return true
	}
	if !isDigits(lenStr) {
		return false
	}
	bits := 32
	if ip.To4() == nil {
		bits = 128
	}
	return atoiOf(lenStr) <= bits
}

func atoiOf(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// firewallProtection applies the CSF-style protections (SecurityController::
// protection → SecurityService::setProtection).
func (s *Server) firewallProtection(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	to := "/security/firewall#fw-protect"
	synRate, err := protectionNum(r.FormValue("syn_rate"), "SYN flood", 100000)
	icmpRate := 0
	if err == nil {
		icmpRate, err = protectionNum(r.FormValue("icmp_rate"), "Ping", 10000)
	}
	var conn, flood []portLimit
	if err == nil {
		conn, err = protectionPortLimits(r.FormValue("conn_limits"), false, "Connection limits")
	}
	if err == nil {
		flood, err = protectionPortLimits(r.FormValue("port_flood"), true, "Port flood")
	}
	exempt := []string{}
	if err == nil {
		for _, e := range strings.FieldsFunc(r.FormValue("exempt"), func(r rune) bool {
			return unicode.IsSpace(r) || r == ','
		}) {
			if e == "" {
				continue
			}
			if !validNetEntry(e) {
				err = fmt.Errorf("Exempt: %q is not an IP address or network.", clip60(e))
				break
			}
			exempt = append(exempt, e)
		}
	}
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	mine := ""
	if r.FormValue("exempt_me") == "1" {
		mine = clientIP(r)
		if !publicAddress(mine) {
			mine = ""
		}
	}
	if mine != "" {
		has := false
		for _, e := range exempt {
			if e == mine {
				has = true
			}
		}
		if !has {
			exempt = append(exempt, mine)
		}
	}
	p := map[string]any{
		"enabled":     r.FormValue("enabled") == "1",
		"invalid":     r.FormValue("invalid") == "1",
		"syn_rate":    synRate,
		"icmp_rate":   icmpRate,
		"conn_limits": conn,
		"port_flood":  flood,
		"exempt":      exempt,
	}
	reply, err := s.agent.Call(r.Context(), "firewall.protection_set", p, 90*time.Second)
	if err != nil {
		agentErrorRedirect(w, r, "firewall protection", err, to)
		return
	}
	sess.Flash("success", s.guardRecord(r, "firewall.protection", reply, p))
	http.Redirect(w, r, to, http.StatusSeeOther)
}
