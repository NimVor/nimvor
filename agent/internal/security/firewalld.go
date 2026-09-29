package security

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// firewalld (AlmaLinux, Rocky Linux, RHEL). Rules are the default zone's
// services, ports and rich rules. Every change is made both at runtime
// and permanently while firewalld runs, and permanently only (with
// firewall-offline-cmd) while it is stopped, so turning it on applies it.

const firewalldUnit = "firewalld.service"

// useFirewalld reports whether this system's firewall is firewalld.
func useFirewalld(env *host.Env) bool {
	return env.Runner.Available("firewall-cmd") && (env.Platform.PackageManager() == "dnf" || !env.Runner.Available("ufw"))
}

func firewalldRunning(ctx context.Context, env *host.Env) bool {
	out, err := env.Runner.Run(ctx, "firewall-cmd", "--state")
	return err == nil && strings.TrimSpace(string(out)) == "running"
}

// firewalldCmds returns the commands making one change: runtime and
// permanent when running, otherwise permanent configuration only.
func firewalldCmds(running bool, arg string) [][]string {
	if running {
		return [][]string{{"firewall-cmd", arg}, {"firewall-cmd", "--permanent", arg}}
	}
	return [][]string{{"firewall-offline-cmd", arg}}
}

func runFirewalld(ctx context.Context, env *host.Env, cmds [][]string) error {
	for _, c := range cmds {
		if c[0] != "firewall-cmd" && c[0] != "firewall-offline-cmd" {
			return fmt.Errorf("unexpected command %s", c[0])
		}
		if out, err := env.Runner.Run(ctx, c[0], c[1:]...); err != nil {
			return actions.Failed("firewalld rejected the change.", host.Output(out))
		}
	}
	return nil
}

var (
	richPortRe   = regexp.MustCompile(`port port="?([0-9]+)(?:-[0-9]+)?"? protocol="?(tcp|udp)"?`)
	richActionRe = regexp.MustCompile(`\b(accept|reject|drop)\b`)
)

func firewalldStatus(ctx context.Context, env *host.Env) (FirewallStatus, error) {
	st := FirewallStatus{Backend: "firewalld", Installed: true, Incoming: "deny", Outgoing: "allow", Rules: []Rule{}}
	st.SSHPort = sshPort(ctx, env)
	st.Active = firewalldRunning(ctx, env)
	tool, perm := "firewall-cmd", []string{}
	if !st.Active {
		if !env.Runner.Available("firewall-offline-cmd") {
			return st, nil
		}
		tool = "firewall-offline-cmd"
	} else {
		// The permanent configuration is what survives; WhaCP keeps both equal.
		perm = []string{"--permanent"}
	}
	list := func(what string) ([]string, error) {
		out, err := env.Runner.Run(ctx, tool, append(append([]string{}, perm...), what)...)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %s", tool, what, host.Output(out))
		}
		if what == "--list-rich-rules" {
			var rules []string
			for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if l = strings.TrimSpace(l); l != "" {
					rules = append(rules, l)
				}
			}
			return rules, nil
		}
		return strings.Fields(string(out)), nil
	}
	ssh := strconv.Itoa(st.SSHPort) + "/tcp"
	services, err := list("--list-services")
	if err != nil {
		return st, err
	}
	for _, s := range services {
		st.Rules = append(st.Rules, Rule{Spec: "service " + s, Args: []string{"--service=" + s}, Action: "allow", SSH: s == "ssh" && st.SSHPort == 22})
	}
	ports, err := list("--list-ports")
	if err != nil {
		return st, err
	}
	for _, p := range ports {
		st.Rules = append(st.Rules, Rule{Spec: "port " + p, Args: []string{"--port=" + p}, Action: "allow", SSH: p == ssh})
	}
	rich, err := list("--list-rich-rules")
	if err != nil {
		return st, err
	}
	for _, rr := range rich {
		r := Rule{Spec: "rich " + rr, Args: []string{"--rich-rule=" + rr}, Action: "allow"}
		if m := richActionRe.FindStringSubmatch(rr); m != nil && m[1] != "accept" {
			r.Action = map[string]string{"reject": "reject", "drop": "deny"}[m[1]]
		}
		if m := richPortRe.FindStringSubmatch(rr); m != nil && r.Action == "allow" && !strings.Contains(rr, "source ") {
			r.SSH = m[1]+"/"+m[2] == ssh
		}
		st.Rules = append(st.Rules, r)
	}
	for i := range st.Rules {
		describeFirewalld(&st.Rules[i])
		st.SSHOpen = st.SSHOpen || st.Rules[i].SSH
	}
	return st, nil
}

// FirewalldArgs turns a validated rule into firewalld items ("--port=…"
// or "--rich-rule=…"), one per protocol.
func (in RuleInput) FirewalldArgs() ([]string, error) {
	if _, err := in.Args(); err != nil {
		return nil, err
	}
	port := strings.Replace(in.Port, ":", "-", 1)
	protos := []string{in.Proto}
	if in.Proto == "" {
		protos = []string{"tcp", "udp"}
	}
	var items []string
	for _, proto := range protos {
		// A plain port covers allow from anywhere; firewalld has no
		// per-source rate limit, so "limit" is an allow.
		if (in.Action == "allow" || in.Action == "limit") && in.From == "" {
			items = append(items, "--port="+port+"/"+proto)
			continue
		}
		rule := "rule"
		if in.From != "" {
			family := "ipv4"
			if strings.Contains(in.From, ":") {
				family = "ipv6"
			}
			rule += ` family="` + family + `" source address="` + in.From + `"`
		}
		action := map[string]string{"allow": "accept", "limit": "accept", "deny": "drop", "reject": "reject"}[in.Action]
		items = append(items, "--rich-rule="+rule+` port port="`+port+`" protocol="`+proto+`" `+action)
	}
	return items, nil
}

// addOrRemove turns "--port=…" into "--add-port=…" or "--remove-port=…".
func addOrRemove(item string, add bool) string {
	verb := "--remove-"
	if add {
		verb = "--add-"
	}
	return verb + strings.TrimPrefix(item, "--")
}

func (s *Service) firewalldAdd(ctx context.Context, in RuleInput) (*Pending, error) {
	items, err := in.FirewalldArgs()
	if err != nil {
		return nil, actions.InvalidParams(err.Error())
	}
	st, err := firewalldStatus(ctx, s.env)
	if err != nil {
		return nil, actions.Failed("The firewall state could not be read.", err.Error())
	}
	have := map[string]bool{}
	for _, r := range st.Rules {
		have[r.Args[0]] = true
	}
	var do, undo [][]string
	for _, it := range items {
		if have[it] {
			continue
		}
		do = append(do, firewalldCmds(st.Active, addOrRemove(it, true))...)
		undo = append(undo, firewalldCmds(st.Active, addOrRemove(it, false))...)
	}
	if len(do) == 0 {
		return nil, actions.Conflict("This rule already exists.")
	}
	if err := runFirewalld(ctx, s.env, do); err != nil {
		runFirewalld(ctx, s.env, undo)
		return nil, err
	}
	return s.guard.Start(Pending{Kind: "firewall", Summary: "firewall: " + strings.Join(items, " "), Commands: undo})
}

func (s *Service) firewalldDelete(ctx context.Context, spec string) (*Pending, error) {
	st, err := firewalldStatus(ctx, s.env)
	if err != nil {
		return nil, actions.Failed("The firewall state could not be read.", err.Error())
	}
	var target *Rule
	sshRules := 0
	for i, r := range st.Rules {
		if r.Spec == spec {
			target = &st.Rules[i]
		}
		if r.SSH {
			sshRules++
		}
	}
	if target == nil {
		return nil, actions.NotFound("The rule no longer exists.")
	}
	if target.SSH && sshRules == 1 && st.Active {
		return nil, actions.Conflict(fmt.Sprintf("This rule keeps SSH (port %d) reachable. Add another rule for SSH before deleting it.", st.SSHPort))
	}
	if msg := keepsCritical(ctx, s.env, st, *target); msg != "" {
		return nil, actions.Conflict(msg)
	}
	do := firewalldCmds(st.Active, addOrRemove(target.Args[0], false))
	undo := firewalldCmds(st.Active, addOrRemove(target.Args[0], true))
	if err := runFirewalld(ctx, s.env, do); err != nil {
		return nil, err
	}
	return s.guard.Start(Pending{Kind: "firewall", Summary: "firewall: delete " + spec, Commands: undo})
}

func (s *Service) firewalldSetEnabled(ctx context.Context, enabled bool, opts EnableOptions) (*Pending, error) {
	st, err := firewalldStatus(ctx, s.env)
	if err != nil {
		return nil, actions.Failed("The firewall state could not be read.", err.Error())
	}
	if st.Active == enabled {
		return nil, nil
	}
	if !enabled {
		if err := s.env.Systemctl(ctx, "stop", firewalldUnit); err != nil {
			return nil, actions.Failed("firewalld did not stop.", err.Error())
		}
		s.env.Systemctl(ctx, "disable", firewalldUnit)
		return s.guard.Start(Pending{Kind: "firewall", Summary: "firewall: disable", Units: [][2]string{{"enable", firewalldUnit}, {"start", firewalldUnit}}})
	}
	// While stopped, rules go into the permanent configuration; undoing runs
	// while firewalld is on (runtime and permanent), before it is stopped.
	have := map[string]bool{}
	for _, r := range st.Rules {
		have[r.Args[0]] = true
	}
	var do, undo [][]string
	var opened []string
	addItems := func(items []string, label string) {
		n := 0
		for _, it := range items {
			if have[it] {
				continue
			}
			have[it] = true
			do = append(do, firewalldCmds(false, addOrRemove(it, true))...)
			undo = append(undo, firewalldCmds(true, addOrRemove(it, false))...)
			n++
		}
		if n > 0 {
			opened = append(opened, label)
		}
	}
	for _, p := range RequiredPorts(ctx, s.env, st.Rules) {
		if p.Open || (!p.Critical && !opts.Services) {
			continue
		}
		if items, err := p.Input().FirewalldArgs(); err == nil {
			addItems(items, p.Service+" ("+p.Port+"/"+p.Proto+")")
		}
	}
	if opts.AllowFrom != "" {
		family := "ipv4"
		if strings.Contains(opts.AllowFrom, ":") {
			family = "ipv6"
		}
		addItems([]string{`--rich-rule=rule family="` + family + `" source address="` + opts.AllowFrom + `" accept`}, "your address "+opts.AllowFrom)
	}
	if err := runFirewalld(ctx, s.env, do); err != nil {
		for _, it := range do {
			rm := append([]string{}, it...)
			rm[len(rm)-1] = strings.Replace(rm[len(rm)-1], "--add-", "--remove-", 1)
			s.env.Runner.Run(ctx, rm[0], rm[1:]...)
		}
		return nil, err
	}
	s.env.Systemctl(ctx, "enable", firewalldUnit)
	if err := s.env.Systemctl(ctx, "start", firewalldUnit); err != nil {
		return nil, actions.Failed("firewalld did not start.", err.Error())
	}
	summary := "firewall: enable"
	if len(opened) > 0 {
		summary += " (opened " + strings.Join(opened, ", ") + ")"
	}
	return s.guard.Start(Pending{Kind: "firewall", Summary: summary, Commands: undo, Units: [][2]string{{"stop", firewalldUnit}, {"disable", firewalldUnit}}})
}
