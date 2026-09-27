package security

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// Rule is a UFW user rule as shown by `ufw show added`.
type Rule struct {
	Spec   string   `json:"spec"` // e.g. "allow 22/tcp"
	Args   []string `json:"-"`
	Action string   `json:"action"`
	SSH    bool     `json:"ssh"` // allows the SSH port
	// Display fields read from the spec.
	Port      string `json:"port"`      // "22", "8000:8100", a service or app name; "" = all
	Proto     string `json:"proto"`     // tcp, udp; "" = both
	From      string `json:"from"`      // "" = anywhere
	Direction string `json:"direction"` // in, out or route
	Comment   string `json:"comment"`
}

// FirewallStatus is the state of the firewall.
type FirewallStatus struct {
	Backend   string     `json:"backend"` // ufw or firewalld
	Installed bool       `json:"installed"`
	Active    bool       `json:"active"`
	Incoming  string     `json:"default_incoming"`
	Outgoing  string     `json:"default_outgoing"`
	Rules     []Rule     `json:"rules"`
	SSHPort   int        `json:"ssh_port"`
	SSHOpen   bool       `json:"ssh_allowed"`
	Listening []Listener `json:"listening"`
}

// SplitArgs splits a ufw rule line, honouring single and double quotes.
func SplitArgs(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inArg, quote := false, rune(0)
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inArg = r, true
		case r == ' ' || r == '\t':
			if inArg {
				out = append(out, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unbalanced quote")
	}
	if inArg {
		out = append(out, cur.String())
	}
	return out, nil
}

var defaultsRe = regexp.MustCompile(`Default:\s*(\w+) \(incoming\),\s*(\w+) \(outgoing\)`)

// ParseAdded parses `ufw show added`.
func ParseAdded(out string, sshPort int) []Rule {
	rules := []Rule{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "ufw ") {
			continue
		}
		spec := strings.TrimPrefix(line, "ufw ")
		args, err := SplitArgs(spec)
		if err != nil || len(args) == 0 {
			continue
		}
		r := Rule{Spec: spec, Args: args, Action: args[0]}
		r.SSH = (r.Action == "allow" || r.Action == "limit") && allowsPort(args, sshPort)
		describeUFW(&r)
		rules = append(rules, r)
	}
	return rules
}

// allowsPort reports whether rule args open port from anywhere.
func allowsPort(args []string, port int) bool {
	p := strconv.Itoa(port)
	from := "any"
	for i, a := range args {
		if a == "from" && i+1 < len(args) {
			from = args[i+1]
		}
	}
	if from != "any" && from != "Anywhere" {
		return false
	}
	for i, a := range args {
		switch {
		case a == p, a == p+"/tcp", strings.EqualFold(a, "OpenSSH") && port == 22:
			return true
		case a == "port" && i+1 < len(args) && args[i+1] == p:
			return true
		}
	}
	return false
}

// sshPort returns the port sshd listens on (22 when unknown).
func sshPort(ctx context.Context, env *host.Env) int {
	if cfg, err := SSHEffective(ctx, env); err == nil {
		if p, err := strconv.Atoi(strings.Fields(cfg["port"] + " 22")[0]); err == nil {
			return p
		}
	}
	return 22
}

// Firewall reads UFW state.
func Firewall(ctx context.Context, env *host.Env) (FirewallStatus, error) {
	if useFirewalld(env) {
		return firewalldStatus(ctx, env)
	}
	st := FirewallStatus{Backend: "ufw", Installed: env.Runner.Available("ufw"), Rules: []Rule{}}
	if !st.Installed {
		return st, nil
	}
	st.SSHPort = sshPort(ctx, env)
	out, err := env.Runner.Run(ctx, "ufw", "status", "verbose")
	if err != nil {
		return st, fmt.Errorf("ufw status failed: %s", host.Output(out))
	}
	s := string(out)
	st.Active = strings.Contains(s, "Status: active")
	if m := defaultsRe.FindStringSubmatch(s); m != nil {
		st.Incoming, st.Outgoing = m[1], m[2]
	}
	added, err := env.Runner.Run(ctx, "ufw", "show", "added")
	if err != nil {
		return st, fmt.Errorf("ufw show added failed: %s", host.Output(added))
	}
	st.Rules = ParseAdded(string(added), st.SSHPort)
	for _, r := range st.Rules {
		st.SSHOpen = st.SSHOpen || r.SSH
	}
	return st, nil
}

// RuleInput describes a rule to add.
type RuleInput struct {
	Action  string `json:"action"` // allow, deny, reject, limit
	Port    string `json:"port"`   // "22" or "8000:8100"
	Proto   string `json:"proto"`  // tcp, udp or "" (both)
	From    string `json:"from"`   // IP/CIDR or "" (anywhere)
	Comment string `json:"comment"`
}

var (
	portRe    = regexp.MustCompile(`^([0-9]{1,5})(:([0-9]{1,5}))?$`)
	commentRe = regexp.MustCompile(`^[A-Za-z0-9 ._:/-]{0,60}$`)
)

// Args validates the input and returns the ufw arguments that add it.
func (in RuleInput) Args() ([]string, error) {
	switch in.Action {
	case "allow", "deny", "reject", "limit":
	default:
		return nil, fmt.Errorf("action must be allow, deny, reject or limit")
	}
	m := portRe.FindStringSubmatch(in.Port)
	if m == nil {
		return nil, fmt.Errorf("port must be a number or a range like 8000:8100")
	}
	lo, _ := strconv.Atoi(m[1])
	if validate.Port("port", lo) != nil {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	if m[3] != "" {
		hi, _ := strconv.Atoi(m[3])
		if validate.Port("port", hi) != nil || hi <= lo {
			return nil, fmt.Errorf("the port range is invalid")
		}
		if in.Proto == "" {
			return nil, fmt.Errorf("a port range needs a protocol (tcp or udp)")
		}
	}
	switch in.Proto {
	case "", "tcp", "udp":
	default:
		return nil, fmt.Errorf("protocol must be tcp, udp or empty")
	}
	from := "any"
	if in.From != "" {
		if validate.IPOrCIDR("from", in.From) != nil {
			return nil, fmt.Errorf("source must be an IP address or network")
		}
		from = in.From
	}
	if !commentRe.MatchString(in.Comment) {
		return nil, fmt.Errorf("comment may contain letters, digits and . _ : / -")
	}
	args := []string{in.Action}
	if in.Proto != "" {
		args = append(args, "proto", in.Proto)
	}
	args = append(args, "from", from, "to", "any", "port", in.Port)
	comment := "whacp"
	if in.Comment != "" {
		comment = "whacp: " + in.Comment
	}
	return append(args, "comment", comment), nil
}

// canonical reduces rule arguments to comparable fields; ufw reorders
// tokens and drops "from any to any" in `ufw show added`.
func canonical(args []string) map[string]string {
	c := map[string]string{"from": "any", "to": "any", "proto": "", "port": "", "app": ""}
	if len(args) > 0 {
		c["action"] = args[0]
	}
	for i := 1; i < len(args); i++ {
		switch a := args[i]; a {
		case "proto", "from", "to", "port", "app":
			if i+1 < len(args) {
				c[a] = args[i+1]
				i++
			}
		case "comment":
			i++
		case "in", "out":
			c["dir"] = a
		default:
			if port, proto, ok := strings.Cut(a, "/"); ok {
				c["port"], c["proto"] = port, proto
			} else if portRe.MatchString(a) {
				c["port"] = a
			} else {
				c["app"] = a
			}
		}
	}
	return c
}

func sameRule(r Rule, args []string) bool {
	a, b := canonical(r.Args), canonical(args)
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// withoutComment strips a trailing "comment X" (not accepted by delete).
func withoutComment(args []string) []string {
	for i, a := range args {
		if a == "comment" {
			return append([]string{}, args[:i]...)
		}
	}
	return args
}

func (s *Service) ufw(ctx context.Context, args ...string) error {
	out, err := s.env.Runner.Run(ctx, "ufw", args...)
	if err != nil {
		return actions.Failed("UFW rejected the change.", host.Output(out))
	}
	return nil
}

// AddRule adds a rule under the guard.
func (s *Service) AddRule(ctx context.Context, in RuleInput) (*Pending, error) {
	args, err := in.Args()
	if err != nil {
		return nil, actions.InvalidParams(err.Error())
	}
	if err := s.guard.Busy(); err != nil {
		return nil, err
	}
	if useFirewalld(s.env) {
		return s.firewalldAdd(ctx, in)
	}
	st, err := Firewall(ctx, s.env)
	if err != nil {
		return nil, actions.Failed("The firewall state could not be read.", err.Error())
	}
	if !st.Installed {
		return nil, actions.Failed("UFW is not installed.", "")
	}
	for _, r := range st.Rules {
		if sameRule(r, args) {
			return nil, actions.Conflict("This rule already exists.")
		}
	}
	out, err := s.env.Runner.Run(ctx, "ufw", args...)
	if err != nil {
		return nil, actions.Failed("UFW rejected the rule.", host.Output(out))
	}
	if strings.Contains(string(out), "Skipping") {
		// ufw kept an identical existing rule; undoing would delete it.
		return nil, actions.Conflict("This rule already exists.")
	}
	summary := "firewall: " + strings.Join(withoutComment(args), " ")
	return s.guard.Start(Pending{Kind: "firewall", Summary: summary, UFW: [][]string{append([]string{"--force", "delete"}, withoutComment(args)...)}})
}

// DeleteRule removes a rule (by its spec from Firewall) under the guard.
func (s *Service) DeleteRule(ctx context.Context, spec string) (*Pending, error) {
	if err := s.guard.Busy(); err != nil {
		return nil, err
	}
	if useFirewalld(s.env) {
		return s.firewalldDelete(ctx, spec)
	}
	st, err := Firewall(ctx, s.env)
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
	if err := s.ufw(ctx, append([]string{"--force", "delete"}, withoutComment(target.Args)...)...); err != nil {
		return nil, err
	}
	return s.guard.Start(Pending{Kind: "firewall", Summary: "firewall: delete " + spec, UFW: [][]string{target.Args}})
}

// SetEnabled turns UFW on or off under the guard. Enabling requires a
// rule that keeps SSH reachable.
func (s *Service) SetEnabled(ctx context.Context, enabled bool) (*Pending, error) {
	if err := s.guard.Busy(); err != nil {
		return nil, err
	}
	if useFirewalld(s.env) {
		return s.firewalldSetEnabled(ctx, enabled)
	}
	st, err := Firewall(ctx, s.env)
	if err != nil {
		return nil, actions.Failed("The firewall state could not be read.", err.Error())
	}
	if !st.Installed {
		return nil, actions.Failed("UFW is not installed.", "Install it with apt install ufw.")
	}
	if st.Active == enabled {
		return nil, nil
	}
	if enabled {
		if !st.SSHOpen {
			return nil, actions.Conflict(fmt.Sprintf("No rule allows SSH (port %d). Add one first, or you would be locked out.", st.SSHPort))
		}
		if err := s.ufw(ctx, "--force", "enable"); err != nil {
			return nil, err
		}
		return s.guard.Start(Pending{Kind: "firewall", Summary: "firewall: enable", UFW: [][]string{{"disable"}}})
	}
	if err := s.ufw(ctx, "disable"); err != nil {
		return nil, err
	}
	return s.guard.Start(Pending{Kind: "firewall", Summary: "firewall: disable", UFW: [][]string{{"--force", "enable"}}})
}
