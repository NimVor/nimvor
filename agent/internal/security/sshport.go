package security

import (
	"context"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/host"
)

// This file holds the checks around an SSH port change. The rule behind
// them: a change is only reported as applied after something on this server
// has actually answered on the new port with an SSH greeting. A firewall rule
// that was added, a configuration file that says so, or a socket that is
// listed as listening prove nothing about that (see docs/administration/ssh-port.md).

// SSHProbe connects to addr ("host:port") and reads the SSH greeting. It is
// a variable so tests need no real server.
var SSHProbe = realSSHProbe

func realSSHProbe(ctx context.Context, addr string) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(4 * time.Second))
	greeting := make([]byte, 4)
	if _, err := io.ReadFull(c, greeting); err != nil {
		return fmt.Errorf("connected, but no SSH greeting arrived: %w", err)
	}
	if string(greeting) != "SSH-" {
		return fmt.Errorf("connected, but the answer is not SSH")
	}
	return nil
}

// ownAddresses lists this server's global addresses, so the probe also
// covers a socket that is bound to specific addresses only. A variable for tests.
var ownAddresses = func() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || !ipn.IP.IsGlobalUnicast() {
			continue
		}
		out = append(out, ipn.IP.String())
	}
	return out
}

// verifyWait is how long a new listener may take to appear.
var verifyWait = 8 * time.Second

// sshMode is how SSH is started on this server.
type sshMode struct {
	SocketActive  bool // ssh.socket is running: systemd listens, "Port" in sshd_config is ignored
	SocketEnabled bool // ssh.socket starts at boot (it may be stopped now)
	IPv6          bool // the kernel has IPv6 (a socket on [::] can be created)
}

func (m sshMode) socket() bool { return m.SocketActive || m.SocketEnabled }

func detectSSHMode(ctx context.Context, env *host.Env) sshMode {
	m := sshMode{SocketActive: sshSocketActive(ctx, env), IPv6: ipv6Usable(env)}
	if !m.SocketActive {
		out, _ := env.Runner.Run(ctx, "systemctl", "is-enabled", "--", sshSocketUnit)
		m.SocketEnabled = strings.TrimSpace(string(out)) == "enabled"
	} else {
		m.SocketEnabled = true
	}
	return m
}

func ipv6Usable(env *host.Env) bool {
	b, err := env.Root.ReadFile("/proc/net/if_inet6")
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return false
	}
	if d, err := env.Root.ReadFile("/proc/sys/net/ipv6/conf/all/disable_ipv6"); err == nil && strings.TrimSpace(string(d)) == "1" {
		return false
	}
	return true
}

// listeners holds the TCP ports in LISTEN state, by family.
type listeners struct {
	v4, v6 map[int]bool
	known  bool
}

func readListeners(env *host.Env) listeners {
	l := listeners{v4: map[int]bool{}, v6: map[int]bool{}}
	for _, f := range []struct {
		file string
		set  map[int]bool
	}{{"/proc/net/tcp", l.v4}, {"/proc/net/tcp6", l.v6}} {
		b, err := env.Root.ReadFile(f.file)
		if err != nil {
			continue
		}
		if f.file == "/proc/net/tcp" {
			l.known = true
		}
		for _, line := range strings.Split(string(b), "\n")[1:] {
			fs := strings.Fields(line)
			if len(fs) < 4 || fs[3] != "0A" { // 0A = LISTEN
				continue
			}
			_, hexPort, ok := strings.Cut(fs[1], ":")
			if !ok {
				continue
			}
			if p, err := strconv.ParseInt(hexPort, 16, 32); err == nil {
				f.set[int(p)] = true
			}
		}
	}
	return l
}

// firewallOpens reports whether the managed firewall (UFW or firewalld)
// has a rule that lets the port in.
func firewallOpens(ctx context.Context, env *host.Env, port int) bool {
	fw, err := Firewall(ctx, env)
	if err != nil || !fw.Active {
		return false
	}
	want := strconv.Itoa(port)
	for _, r := range fw.Rules {
		if r.Action != "allow" && r.Action != "limit" {
			continue
		}
		if fw.Backend == "firewalld" {
			if r.Spec == "port "+want+"/tcp" {
				return true
			}
			continue
		}
		if allowsPort(r.Args, port) {
			return true
		}
	}
	return false
}

var (
	nftDportRe = func(port int) *regexp.Regexp {
		return regexp.MustCompile(`dport\s+(\{[^}]*\b)?` + strconv.Itoa(port) + `\b`)
	}
	ipt4DportRe = func(port int) *regexp.Regexp {
		return regexp.MustCompile(`--dports?\s+([0-9:,]*[,:])?` + strconv.Itoa(port) + `\b`)
	}
)

// unmanagedFilterBlocks looks at nftables and iptables rules NimVor does not
// manage (call it only when neither UFW nor firewalld is active). It returns
// why the port would be closed, or "" when nothing there drops it. NimVor's
// own tables have policy accept and are not a problem.
func unmanagedFilterBlocks(ctx context.Context, env *host.Env, port int) string {
	if env.Runner.Available("nft") {
		if out, err := env.Runner.Run(ctx, "nft", "list", "ruleset"); err == nil {
			if r := nftBlocks(string(out), port); r != "" {
				return r
			}
		}
	}
	for _, tool := range []string{"iptables", "ip6tables"} {
		if !env.Runner.Available(tool) {
			continue
		}
		if out, err := env.Runner.Run(ctx, tool, "-S", "INPUT"); err == nil {
			if r := iptablesBlocks(tool, string(out), port); r != "" {
				return r
			}
		}
	}
	return ""
}

func nftBlocks(ruleset string, port int) string {
	re := nftDportRe(port)
	var table, chain string
	inInput, dropPolicy, allowed := false, false, false
	finish := func() string {
		if inInput && dropPolicy && !allowed {
			return fmt.Sprintf("the nftables chain %s %s drops incoming connections by default and has no rule that accepts TCP port %d", table, chain, port)
		}
		return ""
	}
	for _, line := range strings.Split(ruleset, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "table "):
			table = strings.TrimSuffix(strings.TrimPrefix(t, "table "), " {")
		case strings.HasPrefix(t, "chain "):
			chain = strings.TrimSuffix(strings.TrimPrefix(t, "chain "), " {")
			inInput, dropPolicy, allowed = false, false, false
		case strings.Contains(t, "hook input"):
			inInput = true
			dropPolicy = strings.Contains(t, "policy drop") || strings.Contains(t, "policy reject")
		case t == "}" && inInput:
			if r := finish(); r != "" {
				return r
			}
			inInput = false
		case inInput && strings.Contains(t, "accept") && re.MatchString(t):
			allowed = true
		}
	}
	return finish()
}

func iptablesBlocks(tool, out string, port int) string {
	re := ipt4DportRe(port)
	deny, allowed := false, false
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if t == "-P INPUT DROP" || t == "-P INPUT REJECT" {
			deny = true
		}
		if strings.HasPrefix(t, "-A INPUT") && strings.Contains(t, "-j ACCEPT") && re.MatchString(t) {
			allowed = true
		}
	}
	if deny && !allowed {
		return fmt.Sprintf("%s drops incoming connections by default and has no rule that accepts TCP port %d", tool, port)
	}
	return ""
}

// probeTargets are the addresses a new SSH port must answer on.
func probeTargets(env *host.Env, port int, v6 bool) []string {
	p := strconv.Itoa(port)
	t := []string{net.JoinHostPort("127.0.0.1", p)}
	if v6 {
		t = append(t, net.JoinHostPort("::1", p))
	}
	for _, a := range ownAddresses() {
		if ip := net.ParseIP(a); ip != nil && (ip.To4() != nil || v6) {
			t = append(t, net.JoinHostPort(a, p))
		}
	}
	return t
}

// verifyListening waits until the new port is a listening socket in every
// family that is meant to serve it and until an SSH greeting is received on
// each address. It returns what was verified.
func verifyListening(ctx context.Context, env *host.Env, port int, v6 bool) ([]string, error) {
	deadline := time.Now().Add(verifyWait)
	var last error
	for {
		last = nil
		l := readListeners(env)
		if l.known && !l.v4[port] && !l.v6[port] {
			last = fmt.Errorf("no socket is listening on TCP port %d", port)
		} else if l.known && v6 && !l.v6[port] && !l.v4[port] {
			last = fmt.Errorf("nothing listens on IPv6 port %d", port)
		}
		var ok []string
		if last == nil {
			for _, addr := range probeTargets(env, port, v6) {
				if err := SSHProbe(ctx, addr); err != nil {
					last = fmt.Errorf("%s did not answer: %w", addr, err)
					break
				}
				ok = append(ok, addr)
			}
		}
		if last == nil {
			return ok, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return nil, last
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// socketDropIn renders the ssh.socket drop-in: each family gets its own
// listener, so an IPv4 connection never depends on the kernel's dual-stack
// default (net.ipv6.bindv6only).
func socketDropIn(port int, v6 bool) string {
	var b strings.Builder
	b.WriteString("# Written by NimVor: the port SSH listens on (sshd is started by this socket).\n[Socket]\nListenStream=\n")
	fmt.Fprintf(&b, "ListenStream=0.0.0.0:%d\n", port)
	if v6 {
		fmt.Fprintf(&b, "ListenStream=[::]:%d\nBindIPv6Only=ipv6-only\n", port)
	}
	return b.String()
}

var socketPortRe = regexp.MustCompile(`:(\d+) \(Stream\)`)

// SSHPorts are the TCP ports SSH really answers on. Under socket activation
// that is what ssh.socket listens on; sshd_config's Port is ignored there, so
// `sshd -T` alone can name a port nothing listens on.
func SSHPorts(ctx context.Context, env *host.Env) []int {
	var out []int
	seen := map[int]bool{}
	add := func(p int) {
		if p > 0 && p < 65536 && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if sshSocketActive(ctx, env) {
		if b, err := env.Runner.Run(ctx, "systemctl", "show", "--property=Listen", "--value", "--", sshSocketUnit); err == nil {
			for _, m := range socketPortRe.FindAllStringSubmatch(string(b), -1) {
				p, _ := strconv.Atoi(m[1])
				add(p)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if eff, err := SSHEffective(ctx, env); err == nil {
		for _, f := range strings.Fields(eff["port"]) {
			p, _ := strconv.Atoi(f)
			add(p)
		}
	}
	if len(out) == 0 {
		out = []int{22}
	}
	return out
}
