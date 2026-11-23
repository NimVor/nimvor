package security

import (
	"fmt"
	"strings"

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
			out = append(out, m[1])
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
