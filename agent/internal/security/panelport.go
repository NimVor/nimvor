package security

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/host"
)

// The panel's own HTTPS port is protected: its rule cannot be deleted and
// no rule that blocks it for everyone can be added, from the panel or the
// API. (Someone with root on the server can still use ufw or firewall-cmd
// directly; NimVor cannot stop that.) The port is read from the panel's web
// server site, so it follows a changed panel port.

// panelPorts are the TCP ports of the panel's HTTPS site.
func panelPorts(env *host.Env) []string {
	var out []string
	for _, site := range PanelSites {
		b, err := env.Root.ReadFile(site)
		if err != nil {
			continue
		}
		for _, m := range panelListenRe.FindAllStringSubmatch(string(b), -1) {
			dup := false // IPv4 and IPv6 listen lines name the port twice
			for _, o := range out {
				dup = dup || o == m[1]
			}
			if !dup {
				out = append(out, m[1])
			}
		}
	}
	return out
}

func fromAnywhere(from string) bool { return from == "" || from == "any" || from == "Anywhere" }

// isPanelPortRule: an allow (or rate-limited allow) rule for exactly a panel
// port, for everyone. Wider rules (a range, everything) are covered by the
// "only rule that keeps it open" check instead.
func isPanelPortRule(r Rule, ports []string) string {
	if r.Action != "allow" && r.Action != "limit" || (r.Direction != "" && r.Direction != "in") || !fromAnywhere(r.From) || (r.Proto != "" && r.Proto != "tcp") {
		return ""
	}
	rp := strings.Replace(r.Port, "-", ":", 1)
	for _, p := range ports {
		if rp == p {
			return p
		}
	}
	return ""
}

// markProtected flags the rules the panel's port depends on.
func markProtected(env *host.Env, st *FirewallStatus) {
	ports := panelPorts(env)
	if len(ports) == 0 {
		return
	}
	for i := range st.Rules {
		if p := isPanelPortRule(st.Rules[i], ports); p != "" {
			st.Rules[i].Protected = "NimVor panel (port " + p + ")"
		}
	}
}

func protectedDeleteMessage(r Rule) string {
	return fmt.Sprintf("This rule keeps the %s reachable, so it cannot be removed here. If the panel moves to another port, its old rule can be removed after that.", r.Protected)
}

// blocksPanel refuses a rule that would close the panel's port for everyone.
func blocksPanel(env *host.Env, in RuleInput) string {
	if (in.Action != "deny" && in.Action != "reject") || !fromAnywhere(in.From) || (in.Proto != "" && in.Proto != "tcp") {
		return ""
	}
	m := portRe.FindStringSubmatch(in.Port)
	if m == nil {
		return ""
	}
	lo := atoi(m[1])
	hi := lo
	if m[3] != "" {
		hi = atoi(m[3])
	}
	for _, p := range panelPorts(env) {
		if n := atoi(p); n >= lo && n <= hi {
			return fmt.Sprintf("This rule would block the NimVor panel (port %s) for everyone, so it was not added. Block single addresses instead (give a source address).", p)
		}
	}
	return ""
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// panelWatchEvery is how often the panel's firewall rule is checked.
var panelWatchEvery = time.Minute

// WatchPanelPort restores the panel's firewall rule when it disappeared (someone ran ufw or
// firewall-cmd by hand, a firewall was reset). It runs for the life of the agent.
func (s *Service) WatchPanelPort(ctx context.Context) {
	t := time.NewTicker(panelWatchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.ensurePanelPort(ctx)
		}
	}
}

// opensPort: any rule that allows the TCP port in, from anyone or from chosen addresses. A panel
// that an administrator opened to their own address only counts as open: it is not widened.
func opensPort(rules []Rule, port string) bool {
	for _, r := range rules {
		if r.Action != "allow" && r.Action != "limit" || (r.Direction != "" && r.Direction != "in") || (r.Proto != "" && r.Proto != "tcp") {
			continue
		}
		rp := strings.Replace(r.Port, "-", ":", 1)
		if rp == port || portIn(port, rp) {
			return true
		}
		if names, ok := portAliases[strings.ToLower(rp)]; ok {
			for _, n := range names {
				if n == port+"/tcp" {
					return true
				}
			}
		}
	}
	return false
}

// ensurePanelPort adds the rule of every panel port no allow rule opens, and returns those ports.
// It does nothing while a security change awaits confirmation (that change may be rolled back,
// which would remove what is added now), when the firewall is off, or when it cannot be read.
func (s *Service) ensurePanelPort(ctx context.Context) []string {
	env := s.env
	ports := panelPorts(env)
	if len(ports) == 0 || s.guard.Busy() != nil {
		return nil
	}
	fw, err := Firewall(ctx, env)
	if err != nil || !fw.Active {
		return nil
	}
	var restored []string
	for _, p := range ports {
		if opensPort(fw.Rules, p) {
			continue
		}
		var err error
		if fw.Backend == "firewalld" {
			err = runFirewalld(ctx, env, firewalldCmds(true, addOrRemove("--port="+p+"/tcp", true)))
		} else {
			rule := []string{"allow", "proto", "tcp", "from", "any", "to", "any", "port", p, "comment", "nimvor: panel"}
			// First in the list, so a rule that denies it cannot come before.
			if out, ierr := env.Runner.Run(ctx, "ufw", append([]string{"insert", "1"}, rule...)...); ierr != nil {
				// An empty rule list has no position 1.
				if out2, err2 := env.Runner.Run(ctx, "ufw", rule...); err2 != nil {
					err = fmt.Errorf("%s / %s", host.Output(out), host.Output(out2))
				}
			}
		}
		if err != nil {
			env.Log.Error("the panel's firewall rule is missing and could not be restored", "port", p, "error", err)
			continue
		}
		env.Log.Warn("the panel's firewall rule was missing; it was restored", "port", p)
		restored = append(restored, p)
	}
	return restored
}
