package mailmgr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/limits"
	"github.com/NimVor/nimvor/agent/internal/mailrate"
)

// Outgoing mail limits: NimVor's policy service (see package mailrate)
// counts the recipients of mail each website account submits over SMTP
// from this machine. Website accounts may then not use Postfix's own
// sendmail (authorized_submit_users), which would bypass the count; PHP
// and the commands NimVor runs as an account use NimVor's sendmail. Should
// the service fail, Postfix accepts the mail (default_action=DUNNO).

const (
	MailRateService = "nimvor-mailrate.service"
	MailRateSocket  = "nimvor-mailrate.socket"
	mailRateClause  = "check_policy_service { unix:private/nimvor-mailrate, default_action=DUNNO }"
	submitAll       = "!nobody, static:anyone"
)

// Replaced in tests.
var (
	mailRateControl = "/run/nimvor-mailrate/control"
	mailRateCheck   = mailRateSelfTest
)

func submitLimited(dir string) string {
	return "!nobody, !texthash:" + filepath.Join(dir, limits.NoSubmitFile) + ", static:anyone"
}

func (e env) mailRatePath() string { return e.M.MailDir + "/mailrate.json" }

// mailRateSettings is the configuration as the panel sees it (by account
// name).
type mailRateSettings struct {
	Enabled  bool           `json:"enabled"`
	PerHour  int            `json:"per_hour"`
	Accounts map[string]int `json:"accounts"`
}

func (e env) loadMailRate() (*mailrate.Config, error) {
	c := &mailrate.Config{}
	b, err := e.Root.ReadFile(e.mailRatePath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, c); err != nil {
			return nil, err
		}
	}
	c.Normalize()
	return c, nil
}

// websiteUIDs maps the website accounts' names to UIDs.
func (e env) websiteUIDs() map[string]int {
	out := map[string]int{}
	b, err := e.Root.ReadFile("/etc/passwd")
	if err != nil {
		return out
	}
	accounts := map[string]bool{}
	for _, u := range limits.WebsiteAccounts(e.Env) {
		accounts[u] = true
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 7 || !accounts[f[0]] {
			continue
		}
		if uid, err := strconv.Atoi(f[2]); err == nil {
			out[f[0]] = uid
		}
	}
	return out
}

func (e env) mailRateView(c *mailrate.Config) mailRateSettings {
	v := mailRateSettings{Enabled: c.Enabled, PerHour: c.PerHour, Accounts: map[string]int{}}
	for uid, n := range c.Accounts {
		if name := c.Names[uid]; name != "" {
			v.Accounts[name] = n
		}
	}
	return v
}

func renderMailRateSocket(queueDir string) string {
	return "# Managed by NimVor (outgoing mail limits). Do not edit: changes are overwritten.\n[Unit]\nDescription=NimVor outgoing mail limit policy socket (Postfix)\n\n" +
		"[Socket]\nListenStream=" + queueDir + "/private/nimvor-mailrate\nSocketUser=postfix\nSocketGroup=postfix\nSocketMode=0600\n\n" +
		"[Install]\nWantedBy=sockets.target\n"
}

// renderMailRateService: an unprivileged dynamic user with no
// capabilities. It reads only its own view of the socket table
// (/proc/self/net), so it keeps the host's user and network namespaces
// (no PrivateUsers or PrivateNetwork: owners must be the real UIDs).
func renderMailRateService(bin, configPath string) string {
	return "# Managed by NimVor (outgoing mail limits). Do not edit: changes are overwritten.\n[Unit]\nDescription=NimVor outgoing mail limit policy service (Postfix)\nRequires=" + MailRateSocket + "\nAfter=network.target\n\n" +
		"[Service]\nType=simple\nExecStart=" + bin + " mailrate-policy\n" +
		"LoadCredential=config:" + configPath + "\n" +
		"DynamicUser=yes\nStateDirectory=nimvor-mailrate\nStateDirectoryMode=0700\nRuntimeDirectory=nimvor-mailrate\nRuntimeDirectoryMode=0700\n" +
		"Restart=on-failure\nRestartSec=2\nTimeoutStopSec=20\n" +
		"NoNewPrivileges=yes\nCapabilityBoundingSet=\nAmbientCapabilities=\n" +
		"ProtectSystem=strict\nProtectHome=yes\nPrivateTmp=yes\nPrivateDevices=yes\n" +
		"ProtectKernelTunables=yes\nProtectKernelModules=yes\nProtectKernelLogs=yes\nProtectControlGroups=yes\nProtectClock=yes\nProtectHostname=yes\n" +
		"ProtectProc=invisible\nProcSubset=pid\n" +
		"RestrictAddressFamilies=AF_UNIX\nRestrictNamespaces=yes\nRestrictRealtime=yes\nRestrictSUIDSGID=yes\n" +
		"LockPersonality=yes\nMemoryDenyWriteExecute=yes\nSystemCallArchitectures=native\nSystemCallFilter=@system-service\nSystemCallFilter=~@privileged @resources\nSystemCallErrorNumber=EPERM\n" +
		"UMask=0077\nMemoryMax=128M\nTasksMax=64\n\n" +
		"[Install]\nWantedBy=multi-user.target\n"
}

// withMailRate puts the policy check first in a restriction list (before
// any permit_mynetworks), or removes it.
func withMailRate(current string, on bool) string {
	cur := strings.TrimSpace(strings.ReplaceAll(current, "\n", " "))
	if idx := strings.Index(cur, "check_policy_service { unix:private/nimvor-mailrate"); idx >= 0 {
		rest := ""
		if end := strings.Index(cur[idx:], "}"); end >= 0 {
			rest = cur[idx+end+1:]
		}
		cur = strings.TrimRight(strings.TrimSpace(cur[:idx]), ", ") + ", " + strings.TrimLeft(strings.TrimSpace(rest), ", ")
		cur = strings.Trim(strings.TrimSpace(cur), ", ")
	}
	if !on {
		return cur
	}
	if cur == "" {
		return mailRateClause
	}
	return mailRateClause + ", " + cur
}

func mailRateSelfTest(ctx context.Context, socket string) error {
	var d net.Dialer
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var c net.Conn
	var err error
	for i := 0; i < 20; i++ {
		if c, err = d.DialContext(ctx, "unix", socket); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.Write([]byte("request=smtpd_access_policy\nprotocol_state=RCPT\nclient_address=192.0.2.1\nclient_port=1\n\n")); err != nil {
		return err
	}
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return err
	}
	if strings.TrimSpace(line) != "action=DUNNO" {
		return fmt.Errorf("unexpected answer %q", line)
	}
	return nil
}

func (e env) unitctl(ctx context.Context, verb, unit string) error {
	out, err := e.Runner.Run(ctx, "systemctl", verb, "--", unit)
	if err != nil {
		return fmt.Errorf("systemctl %s %s failed: %s", verb, unit, host.Output(out))
	}
	return nil
}

func registerMailRate(r *actions.Registry, e env) {
	status := func(ctx context.Context, c *mailrate.Config) map[string]any {
		out := map[string]any{
			"config":     e.mailRateView(c),
			"accounts":   limits.WebsiteAccounts(e.Env),
			"service":    map[string]bool{"active": e.UnitActive(ctx, MailRateService), "postfix": strings.Contains(e.postconfValue(ctx, "smtpd_recipient_restrictions"), "unix:private/nimvor-mailrate")},
			"configured": e.configured(),
		}
		if c.Enabled && !e.Cfg.SimulateCommands {
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if st, err := mailrate.Control(cctx, mailRateControl, mailrate.ControlRequest{Op: "status"}); err == nil {
				// Accounts created since the limits were saved.
				names := map[int]string{}
				for n, uid := range e.websiteUIDs() {
					names[uid] = n
				}
				for i := range st.Accounts {
					if st.Accounts[i].Name == "" {
						st.Accounts[i].Name = names[st.Accounts[i].UID]
					}
				}
				out["usage"] = st
			}
		}
		return out
	}
	r.Register(actions.Action{Name: "mail.rate_get", Description: "Outgoing mail limits for website accounts: settings, service state and this hour's recipients per account.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		c, err := e.loadMailRate()
		if err != nil {
			return nil, actions.Failed("The mail limit configuration could not be read.", err.Error())
		}
		return status(ctx, c), nil
	}})

	apply := func(ctx context.Context, c *mailrate.Config) error {
		b, _ := json.MarshalIndent(c, "", "  ")
		dir := e.M.MailDir
		set := e.Changes("mail-rate")
		set.Write(e.mailRatePath(), b, 0o600, agentfs.RootOwner)
		set.Write(filepath.Join(dir, limits.NoSubmitFile), []byte(limits.RenderNoSubmit(limits.WebsiteAccounts(e.Env))), 0o644, agentfs.RootOwner)
		set.Write(filepath.Join(e.M.UnitDir, MailRateSocket), []byte(renderMailRateSocket(e.queueDir(ctx))), 0o644, agentfs.RootOwner)
		set.Write(filepath.Join(e.M.UnitDir, MailRateService), []byte(renderMailRateService(greylistExecutable(), e.mailRatePath())), 0o644, agentfs.RootOwner)
		applied, err := set.Commit(nil)
		if err != nil {
			return host.FailWithValidation("The mail limit configuration was not written", err)
		}
		prevRestr := e.postconfValue(ctx, "smtpd_recipient_restrictions")
		prevSubmit := e.postconfValue(ctx, "authorized_submit_users")
		wasActive := e.UnitActive(ctx, MailRateService)
		fail := func(msg string, cause error) error {
			e.run(ctx, "postconf", "-e", "smtpd_recipient_restrictions="+prevRestr)
			e.run(ctx, "postconf", "-e", "authorized_submit_users="+prevSubmit)
			e.reloadPostfix(ctx)
			applied.Rollback()
			e.DaemonReload(ctx)
			if wasActive {
				e.Systemctl(ctx, "restart", MailRateService)
			}
			detail := cause.Error()
			if j := e.JournalTail(ctx, MailRateService, 8); j != "" {
				detail += "\n" + j
			}
			return actions.Failed(msg+" The previous settings were restored.", detail)
		}
		if err := e.DaemonReload(ctx); err != nil {
			return fail("systemd could not load the mail limit service.", err)
		}
		postfixRunning := e.run(ctx, "postfix", "status") == nil
		if c.Enabled {
			e.unitctl(ctx, "enable", MailRateSocket)
			e.Systemctl(ctx, "enable", MailRateService)
			if err := e.unitctl(ctx, "start", MailRateSocket); err != nil {
				return fail("The mail limit socket did not start.", err)
			}
			if err := e.Systemctl(ctx, "restart", MailRateService); err != nil {
				return fail("The mail limit service did not start.", err)
			}
			if !e.Cfg.SimulateCommands {
				if err := mailRateCheck(ctx, filepath.Join(e.queueDir(ctx), "private", "nimvor-mailrate")); err != nil {
					return fail("The mail limit service does not answer.", err)
				}
			}
			if err := e.run(ctx, "postconf", "-e", "smtpd_recipient_restrictions="+withMailRate(prevRestr, true)); err != nil {
				return fail("Postfix was not configured.", err)
			}
			if err := e.run(ctx, "postconf", "-e", "authorized_submit_users="+submitLimited(dir)); err != nil {
				return fail("Postfix was not configured.", err)
			}
			if err := e.run(ctx, "postfix", "check"); err != nil {
				return fail("Postfix refused the mail limit settings.", err)
			}
			if postfixRunning {
				if err := e.reloadPostfix(ctx); err != nil {
					return fail("Postfix did not reload.", err)
				}
			}
			return nil
		}
		// Off: Postfix stops asking first, then the service stops.
		if err := e.run(ctx, "postconf", "-e", "smtpd_recipient_restrictions="+withMailRate(prevRestr, false)); err != nil {
			return fail("Postfix was not configured.", err)
		}
		if err := e.run(ctx, "postconf", "-e", "authorized_submit_users="+submitAll); err != nil {
			return fail("Postfix was not configured.", err)
		}
		if postfixRunning {
			if err := e.reloadPostfix(ctx); err != nil {
				return fail("Postfix did not reload.", err)
			}
		}
		e.Systemctl(ctx, "disable", MailRateService)
		e.unitctl(ctx, "disable", MailRateSocket)
		e.Systemctl(ctx, "stop", MailRateService)
		e.unitctl(ctx, "stop", MailRateSocket)
		return nil
	}

	r.Register(actions.Action{Name: "mail.rate_set", Mutating: true, Description: "Change outgoing mail limits: on/off, recipients per website account and hour (0: no limit) and per-account limits by account name.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in mailRateSettings
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if !e.configured() {
			return nil, actions.Conflict("Set up email first.")
		}
		mu.Lock()
		defer mu.Unlock()
		uids := e.websiteUIDs()
		c := &mailrate.Config{Enabled: in.Enabled, PerHour: in.PerHour, Accounts: map[string]int{}, Names: map[string]string{}}
		for name, uid := range uids {
			c.Names[strconv.Itoa(uid)] = name
		}
		for name, n := range in.Accounts {
			uid, ok := uids[name]
			if !ok {
				return nil, actions.InvalidParams(name + " is not a website account")
			}
			c.Accounts[strconv.Itoa(uid)] = n
		}
		if err := c.Validate(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		c.Normalize()
		if err := apply(ctx, c); err != nil {
			return nil, err
		}
		return status(ctx, c), nil
	}})

	r.Register(actions.Action{Name: "mail.rate_reset", Mutating: true, Description: "Let a website account (or all with an empty name) send again this hour.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Account string `json:"account"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		uid := -1
		if in.Account != "" {
			u, ok := e.websiteUIDs()[in.Account]
			if !ok {
				return nil, actions.InvalidParams(in.Account + " is not a website account")
			}
			uid = u
		}
		c, err := e.loadMailRate()
		if err != nil {
			return nil, actions.Failed("The mail limit configuration could not be read.", err.Error())
		}
		if !c.Enabled {
			return nil, actions.Conflict("Outgoing mail limits are off.")
		}
		if !e.Cfg.SimulateCommands {
			if _, err := mailrate.Control(ctx, mailRateControl, mailrate.ControlRequest{Op: "reset", UID: uid}); err != nil {
				return nil, actions.Failed("The mail limit service does not answer.", err.Error())
			}
		}
		return status(ctx, c), nil
	}})
}
