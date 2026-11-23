package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Jails NimVor can enable (all shipped with fail2ban's default filters).
var Jails = map[string]string{
	"sshd":            "SSH login failures",
	"recidive":        "Repeat offenders (bans for a week)",
	"nginx-http-auth": "Nginx basic-auth failures",
	"nginx-botsearch": "Scanners probing for common exploits",
	"nginx-limit-req": "Clients exceeding Nginx rate limits",
}

// JailStatus is one running jail.
type JailStatus struct {
	Name        string   `json:"name"`
	Banned      int      `json:"currently_banned"`
	TotalBanned int      `json:"total_banned"`
	Failed      int      `json:"currently_failed"`
	BannedIPs   []string `json:"banned_ips"`
}

// Fail2banStatus is the state of fail2ban.
type Fail2banStatus struct {
	Installed bool         `json:"installed"`
	Running   bool         `json:"running"`
	Jails     []JailStatus `json:"jails"`
	Managed   string       `json:"managed_config,omitempty"`
}

var f2bField = regexp.MustCompile(`(?m)^\W*(Currently failed|Total failed|Currently banned|Total banned|Banned IP list|Jail list):\s*(.*)$`)

func parseF2B(out string) map[string]string {
	m := map[string]string{}
	for _, x := range f2bField.FindAllStringSubmatch(out, -1) {
		m[x[1]] = strings.TrimSpace(x[2])
	}
	return m
}

// Fail2ban reads fail2ban's state.
func Fail2ban(ctx context.Context, env *host.Env) Fail2banStatus {
	st := Fail2banStatus{Installed: env.Runner.Available("fail2ban-client"), Jails: []JailStatus{}}
	if b, err := env.Root.ReadFile(env.M.Fail2banJail); err == nil {
		st.Managed = string(b)
	}
	if !st.Installed {
		return st
	}
	out, err := env.Runner.Run(ctx, "fail2ban-client", "status")
	if err != nil {
		return st
	}
	st.Running = true
	for _, name := range strings.Split(parseF2B(string(out))["Jail list"], ",") {
		name = strings.TrimSpace(name)
		if name == "" || !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(name) {
			continue
		}
		jout, err := env.Runner.Run(ctx, "fail2ban-client", "status", name)
		if err != nil {
			continue
		}
		f := parseF2B(string(jout))
		js := JailStatus{Name: name, BannedIPs: strings.Fields(f["Banned IP list"])}
		js.Banned, _ = strconv.Atoi(f["Currently banned"])
		js.TotalBanned, _ = strconv.Atoi(f["Total banned"])
		js.Failed, _ = strconv.Atoi(f["Currently failed"])
		st.Jails = append(st.Jails, js)
	}
	return st
}

// Fail2banSettings configure the NimVor jail file.
type Fail2banSettings struct {
	Jails         []string `json:"jails"`
	BanMinutes    int      `json:"ban_minutes"`
	FindMinutes   int      `json:"find_minutes"`
	MaxRetry      int      `json:"max_retry"`
	IgnoreAddress []string `json:"ignore"`
	// Journal reads logs from the systemd journal (RHEL: no auth log file).
	Journal bool `json:"-"`
	// SSHPorts are the ports the sshd jail watches and bans on (the SSH server's
	// real ports). Without one the jail keeps fail2ban's own default, port 22.
	SSHPorts []int `json:"-"`
}

// Render produces jail.d/nimvor.local.
func (s Fail2banSettings) Render() (string, error) {
	if s.BanMinutes < 1 || s.BanMinutes > 525600 || s.FindMinutes < 1 || s.FindMinutes > 10080 || s.MaxRetry < 1 || s.MaxRetry > 100 {
		return "", fmt.Errorf("ban time, find time and retries are out of range")
	}
	ignore := []string{"127.0.0.1/8", "::1"}
	if len(s.IgnoreAddress) > 50 {
		return "", fmt.Errorf("at most 50 ignored addresses")
	}
	for _, a := range s.IgnoreAddress {
		if validate.IPOrCIDR("ignore", a) != nil {
			return "", fmt.Errorf("%q is not an IP address or network", a)
		}
		ignore = append(ignore, a)
	}
	jails := append([]string{}, s.Jails...)
	sort.Strings(jails)
	var b strings.Builder
	b.WriteString("# Managed by NimVor (Security → Fail2ban). Do not edit: changes are overwritten.\n")
	fmt.Fprintf(&b, "[DEFAULT]\nbantime = %dm\nfindtime = %dm\nmaxretry = %d\nignoreip = %s\n", s.BanMinutes, s.FindMinutes, s.MaxRetry, strings.Join(ignore, " "))
	if s.Journal {
		b.WriteString("backend = systemd\n")
	}
	for _, j := range jails {
		if _, ok := Jails[j]; !ok {
			return "", fmt.Errorf("unknown jail %q", j)
		}
		fmt.Fprintf(&b, "\n[%s]\nenabled = true\n", j)
		if j == "sshd" && len(s.SSHPorts) > 0 {
			fmt.Fprintf(&b, "port = %s\n", joinPorts(s.SSHPorts))
		}
		if j == "recidive" {
			b.WriteString("bantime = 1w\nfindtime = 1d\n")
		}
	}
	return b.String(), nil
}

// ConfigureFail2ban writes, tests and reloads the jail configuration.
func (s *Service) ConfigureFail2ban(ctx context.Context, in Fail2banSettings) error {
	env := s.env
	if !env.Runner.Available("fail2ban-client") {
		return actions.Failed("fail2ban is not installed.", "Install it under Server → Packages.")
	}
	in.Journal = env.Platform.PackageManager() == "dnf"
	in.SSHPorts = SSHPorts(ctx, env)
	body, err := in.Render()
	if err != nil {
		return actions.InvalidParams(err.Error())
	}
	set := env.Changes("fail2ban")
	set.Write(env.M.Fail2banJail, []byte(body), 0o644, agentfs.RootOwner)
	a, err := set.Commit(func() error {
		out, err := env.Runner.Run(ctx, "fail2ban-client", "--test")
		if err != nil {
			return fmt.Errorf("%s", host.Output(out))
		}
		return nil
	})
	if err != nil {
		return host.FailWithValidation("The fail2ban settings were not applied", err)
	}
	running := env.UnitActive(ctx, fail2banUnit)
	if len(a.Changed) == 0 && running {
		return nil
	}
	if err := applyFail2ban(ctx, env, running); err != nil {
		_ = a.Rollback()
		_ = applyFail2ban(ctx, env, env.UnitActive(ctx, fail2banUnit))
		return actions.Failed("fail2ban could not load the settings; the previous settings were restored.", err.Error())
	}
	return nil
}

const fail2banUnit = "fail2ban.service"

// applyFail2ban loads the jail configuration: a reload when fail2ban runs,
// otherwise a start (a reload needs the running server: "Could not find
// server"). fail2ban reports a failed start only after systemd has started
// it, so this waits until it answers and otherwise returns its own log.
func applyFail2ban(ctx context.Context, env *host.Env, running bool) error {
	if running {
		if out, err := env.Runner.Run(ctx, "fail2ban-client", "reload"); err != nil {
			return errors.New(host.Why(out, err))
		}
		return nil
	}
	_ = env.Systemctl(ctx, "enable", fail2banUnit)
	if err := env.Systemctl(ctx, "restart", fail2banUnit); err != nil {
		return fmt.Errorf("fail2ban did not start: %v", err)
	}
	for i := 0; i < 20; i++ {
		if _, err := env.Runner.Run(ctx, "fail2ban-client", "ping"); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(fail2banWait):
		}
	}
	log, _ := env.Runner.Run(ctx, "journalctl", "--no-pager", "-o", "cat", "-n", "15", "-u", fail2banUnit)
	return fmt.Errorf("fail2ban did not start:\n%s", host.Output(log))
}

// fail2banWait is the pause between checks that fail2ban answers.
var fail2banWait = 500 * time.Millisecond

// Unban removes an address from a jail.
func (s *Service) Unban(ctx context.Context, jail, ip string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`).MatchString(jail) || net.ParseIP(ip) == nil {
		return actions.InvalidParams("jail or IP address is invalid")
	}
	if out, err := s.env.Runner.Run(ctx, "fail2ban-client", "set", jail, "unbanip", ip); err != nil {
		return actions.Failed("The address could not be unbanned.", host.Output(out))
	}
	return nil
}

func joinPorts(ports []int) string {
	var p []string
	for _, n := range ports {
		p = append(p, strconv.Itoa(n))
	}
	return strings.Join(p, ",")
}

var sshdJailPort = regexp.MustCompile(`(?m)^(\[sshd\]\nenabled = true\n)(port = [^\n]*\n)?`)

// withSSHJailPort sets the sshd jail's port in a jail file NimVor wrote. It
// reports false when the file has no enabled sshd jail.
func withSSHJailPort(body string, ports []int) (string, bool) {
	if !sshdJailPort.MatchString(body) {
		return body, false
	}
	return sshdJailPort.ReplaceAllString(body, "${1}port = "+joinPorts(ports)+"\n"), true
}

// syncFail2banSSHPort makes the sshd jail follow the SSH port: its ban rule
// is bound to the jail's port, so after a port change the new port would
// have no brute force protection, and addresses banned on the old port would
// still be refused there. It is best effort (the SSH change does not depend
// on it); what it changed is added to pending so an unconfirmed SSH change
// puts the old port back.
func syncFail2banSSHPort(ctx context.Context, env *host.Env, ports []int, pending *Pending) {
	if !env.Runner.Available("fail2ban-client") {
		return
	}
	cur, err := env.Root.ReadFile(env.M.Fail2banJail)
	if err != nil {
		return
	}
	body, ok := withSSHJailPort(string(cur), ports)
	if !ok || body == string(cur) {
		return
	}
	snap := snapshot(env, env.M.Fail2banJail)
	set := env.Changes("fail2ban-ssh-port")
	set.Write(env.M.Fail2banJail, []byte(body), 0o644, agentfs.RootOwner)
	a, err := set.Commit(func() error {
		out, err := env.Runner.Run(ctx, "fail2ban-client", "--test")
		if err != nil {
			return fmt.Errorf("%s", host.Output(out))
		}
		return nil
	})
	if err != nil {
		env.Log.Warn("fail2ban sshd jail port not updated", "error", err)
		return
	}
	running := env.UnitActive(ctx, fail2banUnit)
	if running {
		if err := applyFail2ban(ctx, env, true); err != nil {
			_ = a.Rollback()
			_ = applyFail2ban(ctx, env, true)
			env.Log.Warn("fail2ban could not load the new sshd jail port", "error", err)
			return
		}
		pending.ReloadUnits = append(pending.ReloadUnits, fail2banUnit)
	}
	pending.Files = append(pending.Files, snap)
}
