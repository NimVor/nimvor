package mailmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/fw"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// smtps is the authenticated mail submission service over TLS from the first byte (port 465): the same rules as
// submission (587, STARTTLS). Many office and hotel networks block 587 and let 465 through.
var smtps = [][2]string{
	{"syslog_name", "postfix/smtps"},
	{"smtpd_tls_wrappermode", "yes"},
	{"smtpd_tls_security_level", "encrypt"},
	{"smtpd_tls_auth_only", "yes"},
	{"smtpd_sasl_auth_enable", "yes"},
	{"smtpd_client_restrictions", "permit_sasl_authenticated,reject"},
	{"smtpd_sender_restrictions", "reject_sender_login_mismatch"},
	{"smtpd_relay_restrictions", "permit_sasl_authenticated,reject"},
	{"milter_macro_daemon_name", "ORIGINATING"},
}

var smtpsService = regexp.MustCompile(`(?m)^smtps\s+inet\s`)

// smtpsOn reports whether Postfix's master.cf has the port 465 service.
func (e env) smtpsOn() bool {
	b, err := e.Root.ReadFile(e.M.PostfixDir + "/master.cf")
	return err == nil && smtpsService.Match(b)
}

var smtpsMu sync.Mutex

func registerSMTPS(r *actions.Registry, e env) {
	r.Register(actions.Action{Name: "mail.smtps_set", Mutating: true, Description: "Turn sending mail on port 465 (SMTP over TLS from the first byte, next to 587 with STARTTLS) on or off. Written into Postfix's master.cf with postconf, checked with postfix check, reloaded and tried with a TLS connection; the previous master.cf is restored if anything fails. Opens the port in the firewall.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		smtpsMu.Lock()
		defer smtpsMu.Unlock()
		if !e.configured() {
			return nil, actions.Conflict("Set up email first.")
		}
		masterCf := e.M.PostfixDir + "/master.cf"
		before, _ := e.Root.ReadFile(masterCf)
		restore := func() {
			if before != nil {
				e.Writer.WriteFile(masterCf, before, 0o644, agentfs.Keep)
			}
			e.reloadPostfix(ctx)
		}
		if !in.Enabled {
			if !e.smtpsOn() {
				return map[string]any{"enabled": false, "changed": false}, nil
			}
			if err := e.run(ctx, "postconf", "-MX", "smtps/inet"); err != nil {
				restore()
				return nil, actions.Failed("Port 465 was not turned off.", err.Error())
			}
			if err := e.run(ctx, "postfix", "check"); err != nil {
				restore()
				return nil, actions.Failed("Port 465 was not turned off: Postfix did not accept the change.", err.Error())
			}
			if err := e.reloadPostfix(ctx); err != nil {
				restore()
				return nil, actions.Failed("Postfix did not reload; port 465 stays as it was.", err.Error())
			}
			return map[string]any{"enabled": false, "changed": true}, nil
		}
		if err := e.run(ctx, "postconf", "-M", "smtps/inet=smtps inet n - y - - smtpd"); err != nil {
			restore()
			return nil, actions.Failed("Port 465 was not turned on.", err.Error())
		}
		for _, kv := range smtps {
			if err := e.run(ctx, "postconf", "-P", "smtps/inet/"+kv[0]+"="+kv[1]); err != nil {
				restore()
				return nil, actions.Failed("Port 465 was not turned on.", err.Error())
			}
		}
		if err := e.run(ctx, "postfix", "check"); err != nil {
			restore()
			return nil, actions.Failed("Port 465 was not turned on: Postfix did not accept the settings.", err.Error())
		}
		if err := e.reloadPostfix(ctx); err != nil {
			restore()
			return nil, actions.Failed("Postfix did not reload; port 465 stays as it was.", err.Error())
		}
		if !e.Cfg.SimulateCommands {
			if err := probe(ctx, "127.0.0.1:465", "220", true); err != nil {
				restore()
				return nil, actions.Failed("Port 465 does not answer. The previous configuration was restored.", fmt.Sprintf("%v\n%s", err, e.JournalTail(ctx, "postfix.service", 10)))
			}
		}
		notes := []string{fw.Open(ctx, e.Env, "mail", "465/tcp")}
		return map[string]any{"enabled": true, "changed": true, "notes": notes}, nil
	}})
}
