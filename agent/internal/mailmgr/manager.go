package mailmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/systemd"
)

// Mail server manager: service state and versions, configuration checks,
// the latest log lines, and a short list of structured Postfix and
// Dovecot settings (no free-form configuration editing). Settings are
// kept in WhaCP's mail data so setting up email again keeps them.

// Tuning holds the structured settings.
type Tuning struct {
	Postfix map[string]string `json:"postfix,omitempty"`
	Dovecot map[string]string `json:"dovecot,omitempty"`
}

type setting struct {
	label string
	check func(string) (string, error) // normalises or rejects a value
}

func intRange(min, max int, suffix string) func(string) (string, error) {
	return func(v string) (string, error) {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < min || n > max {
			return "", fmt.Errorf("a whole number from %d to %d", min, max)
		}
		return strconv.Itoa(n) + suffix, nil
	}
}

func oneOf(values ...string) func(string) (string, error) {
	return func(v string) (string, error) {
		for _, x := range values {
			if v == x {
				return v, nil
			}
		}
		return "", fmt.Errorf("one of %s", strings.Join(values, ", "))
	}
}

var relayRe = regexp.MustCompile(`^\[?([A-Za-z0-9.-]{1,253}|[0-9a-fA-F:]{2,39})\]?(?::(\d{1,5}))?$`)

func relayhost(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	m := relayRe.FindStringSubmatch(v)
	if m == nil || (net.ParseIP(m[1]) == nil && !domainRe.MatchString(strings.ToLower(m[1]))) {
		return "", fmt.Errorf("a host name or address, optionally with :port")
	}
	port := 25
	if m[2] != "" {
		p, _ := strconv.Atoi(m[2])
		if p < 1 || p > 65535 {
			return "", fmt.Errorf("the port must be 1–65535")
		}
		port = p
	}
	return fmt.Sprintf("[%s]:%d", strings.ToLower(m[1]), port), nil
}

var greetingRe = regexp.MustCompile(`^[A-Za-z0-9 ._-]{1,60}$`)

// PostfixSettings are the Postfix parameters WhaCP manages (values as the
// panel shows them; messageSize in MB, lifetimes in days).
var PostfixSettings = map[string]setting{
	"message_size_limit": {"Largest message (MB)", func(v string) (string, error) {
		s, err := intRange(1, 1024, "")(v)
		if err != nil {
			return "", err
		}
		n, _ := strconv.Atoi(s)
		return strconv.Itoa(n << 20), nil
	}},
	"smtpd_recipient_limit":              {"Recipients per message", intRange(1, 10000, "")},
	"maximal_queue_lifetime":             {"Keep retrying undelivered mail (days)", intRange(1, 30, "d")},
	"bounce_queue_lifetime":              {"Keep retrying bounces (days)", intRange(1, 30, "d")},
	"smtpd_client_connection_rate_limit": {"Connections per client per minute (0 = no limit)", intRange(0, 10000, "")},
	"smtpd_client_message_rate_limit":    {"Messages per client per minute (0 = no limit)", intRange(0, 100000, "")},
	"smtpd_helo_required":                {"Require HELO", oneOf("yes", "no")},
	"disable_vrfy_command":               {"Disable VRFY", oneOf("yes", "no")},
	"smtp_tls_security_level":            {"TLS for outgoing mail", oneOf("may", "encrypt", "dane")},
	"inet_protocols":                     {"IP versions", oneOf("all", "ipv4", "ipv6")},
	"relayhost":                          {"Send outgoing mail through (smart host)", relayhost},
	"authorized_submit_users":            {"Local accounts that may send mail with sendmail", oneOf("!nobody, static:anyone", "static:anyone")},
}

// DovecotSettings are the Dovecot settings WhaCP manages.
var DovecotSettings = map[string]setting{
	"mail_max_userip_connections": {"IMAP connections per user and address", intRange(1, 500, "")},
	"auth_failure_delay":          {"Delay after a failed login (seconds)", intRange(0, 30, "s")},
	"ssl_min_protocol":            {"Oldest TLS version", oneOf("TLSv1.2", "TLSv1.3")},
	"login_greeting": {"Login greeting", func(v string) (string, error) {
		v = strings.TrimSpace(v)
		if !greetingRe.MatchString(v) {
			return "", fmt.Errorf("letters, digits, spaces and . _ - (up to 60)")
		}
		return v, nil
	}},
}

func sortedSettingKeys(m map[string]setting) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (e env) tuningDropIn() string {
	return filepath.Join(filepath.Dir(e.M.DovecotDropIn), "99-whacp-tuning.conf")
}

// renderDovecotTuning writes the Dovecot settings (same syntax in 2.3 and 2.4).
func renderDovecotTuning(t map[string]string) string {
	var b strings.Builder
	b.WriteString("# Managed by WhaCP (mail server settings). Do not edit: changes are overwritten.\n")
	for _, k := range []string{"auth_failure_delay", "ssl_min_protocol", "login_greeting"} {
		if v, ok := t[k]; ok {
			fmt.Fprintf(&b, "%s = %s\n", k, v)
		}
	}
	if v, ok := t["mail_max_userip_connections"]; ok {
		fmt.Fprintf(&b, "protocol imap {\n  mail_max_userip_connections = %s\n}\n", v)
	}
	return b.String()
}

// applyPostfixTuning sets the Postfix values (after the base settings).
func (e env) applyPostfixTuning(ctx context.Context, t map[string]string) error {
	for _, k := range sortedTuningKeys(t) {
		if err := e.run(ctx, "postconf", "-e", k+"="+t[k]); err != nil {
			return err
		}
	}
	return nil
}

func sortedTuningKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// postfixSeconds reads a Postfix time value (default unit: days).
func postfixSeconds(v string) int {
	v = strings.TrimSpace(v)
	unit := 86400
	if n := len(v); n > 0 {
		switch v[n-1] {
		case 's':
			unit, v = 1, v[:n-1]
		case 'm':
			unit, v = 60, v[:n-1]
		case 'h':
			unit, v = 3600, v[:n-1]
		case 'd':
			v = v[:n-1]
		case 'w':
			unit, v = 7*86400, v[:n-1]
		}
	}
	n, _ := strconv.Atoi(v)
	return n * unit
}

func (e env) postconfValue(ctx context.Context, key string) string {
	out, err := e.Runner.Run(ctx, "postconf", "-h", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func registerManager(r *actions.Registry, e env) {
	r.Register(actions.Action{Name: "mail.server_status", Description: "Mail services (Postfix, Dovecot, OpenDKIM, Rspamd): running, enabled, version; configuration checks; the managed Postfix and Dovecot settings.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		services := []map[string]any{}
		version := func(name string, args ...string) string {
			out, err := e.Runner.Run(ctx, name, args...)
			if err != nil {
				return ""
			}
			return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		}
		for _, s := range []struct{ unit, name, version string }{
			{"postfix.service", "Postfix", e.postconfValue(ctx, "mail_version")},
			{"dovecot.service", "Dovecot", version("dovecot", "--version")},
			{DKIMUnit, "OpenDKIM", ""},
			{"rspamd.service", "Rspamd", ""},
		} {
			enabled := false
			if out, err := e.Runner.Run(ctx, "systemctl", "is-enabled", "--", s.unit); err == nil {
				enabled = strings.TrimSpace(string(out)) == "enabled"
			}
			// Not installed (Rspamd) or not set up yet (WhaCP's OpenDKIM unit
			// is written when email is set up): nothing to start.
			installed := true
			if st, err := (systemd.Client{Runner: e.Runner}).Status(ctx, []string{s.unit}); err == nil && (len(st) == 0 || st[0].LoadState == "not-found") {
				installed = false
			}
			services = append(services, map[string]any{"unit": s.unit, "name": s.name, "active": e.UnitActive(ctx, s.unit), "enabled": enabled, "version": s.version, "installed": installed})
		}
		checks := map[string]string{}
		if out, err := e.Runner.Run(ctx, "postfix", "check"); err != nil {
			checks["postfix"] = host.Output(out)
		} else {
			checks["postfix"] = ""
		}
		if out, err := e.Runner.Run(ctx, "doveconf", "-n"); err != nil {
			checks["dovecot"] = host.Output(out)
		} else {
			checks["dovecot"] = ""
		}
		d, _ := e.load()
		configured := e.configured()
		values := map[string]map[string]string{"postfix": {}, "dovecot": {}}
		for _, k := range sortedSettingKeys(PostfixSettings) {
			values["postfix"][k] = e.postconfValue(ctx, k)
		}
		for _, k := range sortedSettingKeys(DovecotSettings) {
			values["dovecot"][k] = d.Tuning.Dovecot[k]
		}
		labels := map[string]map[string]string{"postfix": {}, "dovecot": {}}
		for k, s := range PostfixSettings {
			labels["postfix"][k] = s.label
		}
		for k, s := range DovecotSettings {
			labels["dovecot"][k] = s.label
		}
		return map[string]any{"services": services, "checks": checks, "values": values, "labels": labels, "configured": configured}, nil
	}})

	r.Register(actions.Action{Name: "mail.settings_set", Mutating: true, Description: "Change managed Postfix settings (postconf, postfix check, reload) and Dovecot settings (drop-in, doveconf -n, reload); the previous values come back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Postfix map[string]string `json:"postfix"`
			Dovecot map[string]string `json:"dovecot"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		pf := map[string]string{}
		for k, v := range in.Postfix {
			s, ok := PostfixSettings[k]
			if !ok {
				return nil, actions.InvalidParams("unknown Postfix setting " + k)
			}
			nv, err := s.check(v)
			if err != nil {
				return nil, actions.InvalidParams(s.label + ": " + err.Error())
			}
			pf[k] = nv
		}
		dc := map[string]string{}
		for k, v := range in.Dovecot {
			s, ok := DovecotSettings[k]
			if !ok {
				return nil, actions.InvalidParams("unknown Dovecot setting " + k)
			}
			if strings.TrimSpace(v) == "" {
				continue // back to Dovecot's default
			}
			nv, err := s.check(v)
			if err != nil {
				return nil, actions.InvalidParams(s.label + ": " + err.Error())
			}
			dc[k] = nv
		}
		if !e.configured() {
			return nil, actions.Conflict("Set up email first.")
		}
		if _, ok1 := pf["bounce_queue_lifetime"]; ok1 || pf["maximal_queue_lifetime"] != "" {
			// Compare with the value Postfix has when only one of them changes.
			days := func(k string) int {
				v, ok := pf[k]
				if !ok {
					v = e.postconfValue(ctx, k)
				}
				return postfixSeconds(v)
			}
			if days("bounce_queue_lifetime") > days("maximal_queue_lifetime") {
				return nil, actions.InvalidParams("bounces cannot be retried longer than other mail")
			}
		}
		mu.Lock()
		defer mu.Unlock()
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		// Postfix: remember the current values, apply, check, reload.
		prev := map[string]string{}
		for k := range pf {
			prev[k] = e.postconfValue(ctx, k)
		}
		restorePostfix := func() {
			for k, v := range prev {
				e.run(ctx, "postconf", "-e", k+"="+v)
			}
		}
		if len(pf) > 0 {
			if err := e.applyPostfixTuning(ctx, pf); err != nil {
				restorePostfix()
				return nil, actions.Failed("The Postfix settings were not applied.", err.Error())
			}
			if err := e.run(ctx, "postfix", "check"); err != nil {
				restorePostfix()
				return nil, actions.Failed("Postfix refused the settings; the previous ones were restored.", err.Error())
			}
		}
		// Dovecot: its own drop-in, validated.
		set := e.Changes("mail-tuning")
		if d.Tuning.Postfix == nil {
			d.Tuning.Postfix = map[string]string{}
		}
		for k, v := range pf {
			d.Tuning.Postfix[k] = v
		}
		d.Tuning.Dovecot = dc
		b, _ := json.MarshalIndent(d, "", "  ")
		set.Write(e.dataPath(), b, 0o600, agentfs.RootOwner)
		set.Write(e.tuningDropIn(), []byte(renderDovecotTuning(dc)), 0o644, agentfs.RootOwner)
		applied, err := set.Commit(func() error {
			if out, err := e.Runner.Run(ctx, "doveconf", "-n"); err != nil {
				return fmt.Errorf("doveconf: %s", host.Output(out))
			}
			return nil
		})
		if err != nil {
			restorePostfix()
			return nil, host.FailWithValidation("The Dovecot settings were not applied", err)
		}
		if len(pf) > 0 && e.run(ctx, "postfix", "status") == nil {
			if err := e.reloadPostfix(ctx); err != nil {
				applied.Rollback()
				restorePostfix()
				e.reloadPostfix(ctx)
				return nil, actions.Failed("Postfix did not reload; the previous settings were restored.", err.Error())
			}
		}
		if e.UnitActive(ctx, "dovecot.service") {
			if err := e.Systemctl(ctx, "reload", "dovecot.service"); err != nil {
				applied.Rollback()
				restorePostfix()
				e.Systemctl(ctx, "reload", "dovecot.service")
				return nil, actions.Failed("Dovecot did not reload; the previous settings were restored.", err.Error())
			}
		}
		return map[string]any{"postfix": pf, "dovecot": dc}, nil
	}})

	r.Register(actions.Action{Name: "mail.log_tail", Description: "The latest mail log lines (Postfix, Dovecot, OpenDKIM), newest last.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Lines int `json:"lines"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if in.Lines <= 0 || in.Lines > 1000 {
			in.Lines = 200
		}
		lines, source := e.mailLog(ctx, e.Now().Add(-7*24*3600e9), e.Now().Add(60e9))
		if len(lines) > in.Lines {
			lines = lines[len(lines)-in.Lines:]
		}
		out := make([]map[string]any, 0, len(lines))
		for _, l := range lines {
			out = append(out, map[string]any{"time": l.Time, "ident": l.Ident, "message": l.Msg})
		}
		return map[string]any{"lines": out, "source": source}, nil
	}})
}
