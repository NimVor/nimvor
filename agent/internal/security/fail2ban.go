package security

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// Jails WhaCP can enable (all shipped with fail2ban's default filters).
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

// Fail2banSettings configure the WhaCP jail file.
type Fail2banSettings struct {
	Jails         []string `json:"jails"`
	BanMinutes    int      `json:"ban_minutes"`
	FindMinutes   int      `json:"find_minutes"`
	MaxRetry      int      `json:"max_retry"`
	IgnoreAddress []string `json:"ignore"`
}

// Render produces jail.d/whacp.local.
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
	b.WriteString("# Managed by WhaCP (Security → Fail2ban). Do not edit: changes are overwritten.\n")
	fmt.Fprintf(&b, "[DEFAULT]\nbantime = %dm\nfindtime = %dm\nmaxretry = %d\nignoreip = %s\n", s.BanMinutes, s.FindMinutes, s.MaxRetry, strings.Join(ignore, " "))
	for _, j := range jails {
		if _, ok := Jails[j]; !ok {
			return "", fmt.Errorf("unknown jail %q", j)
		}
		fmt.Fprintf(&b, "\n[%s]\nenabled = true\n", j)
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
		return actions.Failed("fail2ban is not installed.", "Install it from Packages or with apt install fail2ban.")
	}
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
	if len(a.Changed) == 0 {
		return nil
	}
	if out, err := env.Runner.Run(ctx, "fail2ban-client", "reload"); err != nil {
		_ = a.Rollback()
		_, _ = env.Runner.Run(ctx, "fail2ban-client", "reload")
		return actions.Failed("fail2ban could not reload; the previous settings were restored.", host.Output(out))
	}
	return nil
}

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
