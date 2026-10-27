package mailmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Spam filtering with Rspamd. NimVor only writes files in Rspamd's
// local.d folder (meant for local settings), marked as its own; it does
// not replace files there it did not write unless confirmed. Rspamd runs
// as a milter in front of Postfix: messages above the "add header" score
// get "X-Spam: Yes" and are filed into Junk by Dovecot's Sieve rule;
// messages above the "reject" score are refused while the sender is still
// connected. Rspamd's own DKIM signing is turned off (OpenDKIM signs).

const (
	rspamdPort  = 11332
	rspamdUnit  = "rspamd.service"
	rspamdLocal = "/etc/rspamd/local.d"
	spamMarker  = "# Managed by NimVor (spam filter). Do not edit: changes are overwritten."
)

// DefaultSpam are the usual thresholds.
func DefaultSpam() Spam { return Spam{AddHeader: 6, Reject: 15} }

func (s Spam) validate() error {
	if s.AddHeader < 1 || s.AddHeader > 50 || s.Reject <= s.AddHeader || s.Reject > 100 {
		return fmt.Errorf("the marking score must be 1–50 and the rejection score higher than it (at most 100)")
	}
	for field, list := range map[string][]string{"no_filter_domains": s.NoFilterDomains, "no_junk_domains": s.NoJunkDomains} {
		if len(list) > 10000 {
			return fmt.Errorf("%s: too many domains", field)
		}
		for _, d := range list {
			if err := validate.Domain(field, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// rspamdFiles are NimVor's local.d files.
func rspamdFiles(s Spam) map[string]string {
	num := func(f float64) string { return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", f), "0"), ".") }
	return map[string]string{
		"actions.conf": spamMarker + "\nreject = " + num(s.Reject) + ";\nadd_header = " + num(s.AddHeader) + ";\n" +
			"rewrite_subject = null;\ngreylist = null;\n",
		"worker-proxy.inc": spamMarker + "\nbind_socket = \"127.0.0.1:" + fmt.Sprint(rspamdPort) + "\";\nmilter = yes;\ntimeout = 120s;\n" +
			"upstream \"local\" {\n  default = yes;\n  self_scan = yes;\n}\nspam_header = \"X-Spam\";\n",
		// Senders cannot mark their own mail to avoid (or force) filtering.
		"milter_headers.conf": spamMarker + "\nuse = [\"authentication-results\"];\nremove_upstream_spam_flag = true;\n",
		// OpenDKIM signs outgoing mail.
		"dkim_signing.conf": spamMarker + "\nenabled = false;\n",
		// Accounts that turned the filter off: their domains are not scanned.
		"settings.conf": spamMarker + "\n" + noFilterSettings(s.NoFilterDomains),
	}
}

func noFilterSettings(domains []string) string {
	if len(domains) == 0 {
		return ""
	}
	rcpt := make([]string, len(domains))
	for i, d := range domains {
		rcpt[i] = `"@` + d + `"`
	}
	return "nimvor_no_filter {\n  priority = high;\n  rcpt = [" + strings.Join(rcpt, ", ") + "];\n  want_spam = yes;\n}\n"
}

func (e env) rspamdInstalled() bool {
	return e.Root.Exists("/usr/bin/rspamd") || e.Root.Exists("/usr/sbin/rspamd")
}

// registerSpam adds mail.spam_configure.
func registerSpam(r *actions.Registry, e env) {
	r.Register(actions.Action{Name: "mail.spam_configure", Mutating: true, Description: "Turn the spam filter (Rspamd) on or off and set the scores at which mail is filed into Junk or refused. Writes only NimVor's files in Rspamd's local.d (replacing other files there needs confirm_replace), validates with rspamadm configtest, restarts Rspamd and updates Postfix; rolled back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		in := struct {
			Enabled         bool     `json:"enabled"`
			AddHeader       float64  `json:"add_header"`
			Reject          float64  `json:"reject"`
			ConfirmReplace  bool     `json:"confirm_replace"`
			NoFilterDomains []string `json:"no_filter_domains"`
			NoJunkDomains   []string `json:"no_junk_domains"`
		}{AddHeader: DefaultSpam().AddHeader, Reject: DefaultSpam().Reject}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		s := Spam{Enabled: in.Enabled, AddHeader: in.AddHeader, Reject: in.Reject, NoFilterDomains: in.NoFilterDomains, NoJunkDomains: in.NoJunkDomains}
		if err := s.validate(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		mu.Lock()
		defer mu.Unlock()
		if !e.configured() {
			return nil, actions.Conflict("Set up email first.")
		}
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		files := rspamdFiles(s)
		set := e.Changes("mail-spam")
		if s.Enabled {
			if !e.rspamdInstalled() {
				return nil, actions.Conflict("Rspamd is not installed. Install it under Server → Packages (rspamd) first.")
			}
			var foreign []string
			for name := range files {
				if b, err := e.Root.ReadFile(rspamdLocal + "/" + name); err == nil && !strings.Contains(string(b), spamMarker) {
					foreign = append(foreign, name)
				}
			}
			if len(foreign) > 0 && !in.ConfirmReplace {
				return nil, actions.Conflict("Rspamd's local.d already has " + strings.Join(foreign, ", ") + ", not written by NimVor. Confirm to replace (a backup is kept).")
			}
			for name, content := range files {
				set.Write(rspamdLocal+"/"+name, []byte(content), 0o644, agentfs.RootOwner)
			}
		}
		prev := d.Spam
		d.Spam = s
		e.writeData(set, d)
		sieve := e.M.MailDir + "/sieve/spam.sieve"
		set.Write(sieve, []byte(RenderSpamSieve(s.NoJunkDomains)), 0o644, agentfs.RootOwner)
		applied, err := set.Commit(func() error {
			// Compiled here: Dovecot cannot write next to the script.
			if err := e.run(ctx, "sievec", sieve); err != nil {
				return err
			}
			if s.Enabled {
				return e.run(ctx, "rspamadm", "configtest")
			}
			return nil
		})
		if err != nil {
			return nil, host.FailWithValidation("The spam filter settings were not applied", err)
		}
		fail := func(msg string, cause error) (any, error) {
			applied.Rollback()
			e.run(ctx, "postconf", "-e", "smtpd_milters="+smtpdMilters(prev.Enabled))
			if prev.Enabled {
				e.Systemctl(ctx, "restart", rspamdUnit)
			}
			e.reloadPostfix(ctx)
			return nil, actions.Failed(msg+" The previous settings were restored.", cause.Error())
		}
		if s.Enabled {
			e.Systemctl(ctx, "enable", rspamdUnit)
			if err := e.Systemctl(ctx, "restart", rspamdUnit); err != nil {
				return fail("Rspamd did not start.", err)
			}
			if !e.Cfg.SimulateCommands {
				var derr error
				for i := 0; i < 60; i++ {
					var c net.Conn
					if c, derr = net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", rspamdPort), 2*time.Second); derr == nil {
						c.Close()
						break
					}
					time.Sleep(250 * time.Millisecond)
				}
				if derr != nil {
					return fail("Rspamd does not listen for Postfix.", derr)
				}
			}
		}
		if err := e.run(ctx, "postconf", "-e", "smtpd_milters="+smtpdMilters(s.Enabled)); err != nil {
			return fail("Postfix could not be updated.", err)
		}
		if err := e.run(ctx, "postfix", "check"); err != nil {
			return fail("Postfix rejected the change.", err)
		}
		if e.run(ctx, "postfix", "status") == nil {
			if err := e.reloadPostfix(ctx); err != nil {
				return fail("Postfix did not reload.", err)
			}
		}
		return map[string]any{"spam": s}, nil
	}})
}
