package security

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// sshSim is a small model of an Ubuntu server's SSH and firewall state, driven
// by the files the agent writes. It answers the way the real tools do, and
// SSHProbe "connects" only where the model has a listener, so a test fails
// the way a real change fails: SSH answers on the wrong port.
type sshSim struct {
	t             *testing.T
	h             *testhost.Host
	socketActive  bool
	socketEnabled bool
	ufw           []string // "allow proto tcp from any to any port 22"
	ufwOn         bool
	deaf          bool // the socket restart does not bind the new port (the incident's symptom)
	probes        []string
}

func newSSHSim(t *testing.T) (*sshSim, *Service) {
	t.Helper()
	stubSSHProbe(t)
	h := testhost.New(t)
	sim := &sshSim{t: t, h: h, ufwOn: true, ufw: []string{"allow proto tcp from any to any port 22"}}
	h.Respond = sim.respond
	h.Runner.Outputs = map[string]string{}
	SSHProbe = func(_ context.Context, addr string) error {
		sim.probes = append(sim.probes, addr)
		_, p, _ := net.SplitHostPort(addr)
		port, _ := strconv.Atoi(p)
		if sim.listensOn(port) {
			return nil
		}
		return fmt.Errorf("dial tcp %s: connect: connection refused", addr)
	}
	adminWithKey(t, h)
	return sim, NewService(h.Env)
}

var portLine = regexp.MustCompile(`(?m)^Port (\d+)`)
var listenLine = regexp.MustCompile(`ListenStream=0\.0\.0\.0:(\d+)`)

// sshdPort is what sshd_config says (what `sshd -T` prints).
func (s *sshSim) sshdPort() int {
	if m := portLine.FindStringSubmatch(s.h.Read("etc/ssh/sshd_config.d/00-nimvor.conf")); m != nil {
		p, _ := strconv.Atoi(m[1])
		return p
	}
	return 22
}

func (s *sshSim) socketPort() int {
	if m := listenLine.FindStringSubmatch(s.h.Read("etc/systemd/system/ssh.socket.d/nimvor-port.conf")); m != nil {
		p, _ := strconv.Atoi(m[1])
		return p
	}
	return 22
}

// listensOn: under socket activation the socket decides and sshd_config's Port is ignored.
func (s *sshSim) listensOn(port int) bool {
	if s.socketActive {
		return !s.deaf && port == s.socketPort() || s.deaf && port == 22 && s.socketPort() == 22
	}
	return port == s.sshdPort()
}

func (s *sshSim) respond(id string, args []string) (string, error, bool) {
	key := id + " " + strings.Join(args, " ")
	switch {
	case key == "sshd -T":
		return fmt.Sprintf("port %d\npermitrootlogin without-password\npasswordauthentication no\nmaxauthtries 4\nx11forwarding no\n", s.sshdPort()), nil, true
	case key == "systemctl is-active -- ssh.socket":
		if s.socketActive {
			return "active\n", nil, true
		}
		return "inactive\n", fmt.Errorf("exit status 3"), true
	case key == "systemctl is-enabled -- ssh.socket":
		if s.socketEnabled || s.socketActive {
			return "enabled\n", nil, true
		}
		return "disabled\n", fmt.Errorf("exit status 1"), true
	case key == "systemctl show --property=Listen --value -- ssh.socket":
		p := s.socketPort()
		if s.deaf {
			p = 22
		}
		return fmt.Sprintf("[::]:%d (Stream)\n0.0.0.0:%d (Stream)\n", p, p), nil, true
	case key == "ufw status verbose":
		if s.ufwOn {
			return ufwActive, nil, true
		}
		return "Status: inactive\n", nil, true
	case key == "ufw show added":
		var b strings.Builder
		b.WriteString("Added user rules (see 'ufw status' for running firewall):\n")
		for _, r := range s.ufw {
			b.WriteString("ufw " + r + "\n")
		}
		return b.String(), nil, true
	case id == "ufw" && len(args) > 0 && (args[0] == "allow" || args[0] == "limit"):
		s.ufw = append(s.ufw, strings.Join(withoutComment(args), " "))
		return "Rule added", nil, true
	case id == "ufw" && len(args) > 2 && args[0] == "--force" && args[1] == "delete":
		want := strings.Join(args[2:], " ")
		for i, r := range s.ufw {
			if r == want {
				s.ufw = append(s.ufw[:i], s.ufw[i+1:]...)
				break
			}
		}
		return "Rule deleted", nil, true
	}
	return "", nil, false
}

func (s *sshSim) hasUFWPort(port int) bool {
	for _, r := range s.ufw {
		if strings.HasSuffix(r, "port "+strconv.Itoa(port)) {
			return true
		}
	}
	return false
}

func (s *sshSim) enableIPv6() {
	os.MkdirAll(filepath.Join(s.h.Base, "proc/net"), 0o755)
	os.WriteFile(filepath.Join(s.h.Base, "proc/net/if_inet6"), []byte("00000000000000000000000000000001 01 80 10 80       lo\n"), 0o644)
}

func (s *sshSim) callIndex(prefix string) int {
	for i, c := range s.h.Runner.Calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func sshWant(port int) SSHSettings {
	return SSHSettings{Port: port, PermitRootLogin: "prohibit-password", PasswordAuthentication: false, MaxAuthTries: 4}
}

func fastConfirm(t *testing.T) {
	old := ConfirmWindow
	ConfirmWindow = 200 * time.Millisecond
	t.Cleanup(func() { ConfirmWindow = old })
}

// Default port 22 to 2020 with the plain sshd service: the firewall is opened
// first, the config is validated, and only a verified listener is reported as success.
func TestSSHPortChangeTraditionalService(t *testing.T) {
	fastConfirm(t)
	sim, s := newSSHSim(t)
	p, err := s.ConfigureSSH(context.Background(), sshWant(2020))
	if err != nil {
		t.Fatal(err)
	}
	if !sim.hasUFWPort(2020) {
		t.Fatalf("no firewall rule for 2020: %v", sim.ufw)
	}
	ufwAt, testAt, reloadAt := sim.callIndex("ufw allow"), sim.callIndex("sshd -t"), sim.callIndex("systemctl reload")
	if ufwAt < 0 || testAt < 0 || reloadAt < 0 || !(ufwAt < testAt && testAt < reloadAt) {
		t.Fatalf("order must be firewall (%d), sshd -t (%d), reload (%d): %v", ufwAt, testAt, reloadAt, sim.h.Runner.Calls)
	}
	if len(p.Verified) == 0 || p.Verified[0] != "127.0.0.1:2020" {
		t.Fatalf("verified = %v", p.Verified)
	}
	if sim.callIndex("systemctl restart -- ssh.socket") >= 0 {
		t.Fatal("the socket must not be touched when it is not in use")
	}
	// Unconfirmed: everything is put back, firewall included.
	waitRollback(t, s)
	if sim.sshdPort() != 22 || sim.hasUFWPort(2020) {
		t.Fatalf("not rolled back: port %d, rules %v", sim.sshdPort(), sim.ufw)
	}
}

// Under socket activation sshd_config's Port is ignored: the socket's own setting must change, for IPv4 and IPv6.
func TestSSHPortChangeSocketActivationDualStack(t *testing.T) {
	fastConfirm(t)
	sim, s := newSSHSim(t)
	sim.socketActive = true
	sim.enableIPv6()
	p, err := s.ConfigureSSH(context.Background(), sshWant(2020))
	if err != nil {
		t.Fatal(err)
	}
	body := sim.h.Read("etc/systemd/system/ssh.socket.d/nimvor-port.conf")
	for _, want := range []string{"ListenStream=\n", "ListenStream=0.0.0.0:2020\n", "ListenStream=[::]:2020\n", "BindIPv6Only=ipv6-only\n"} {
		if !strings.Contains(body, want) {
			t.Fatalf("socket drop-in lacks %q:\n%s", want, body)
		}
	}
	if sim.callIndex("systemctl restart -- ssh.socket") < 0 || sim.callIndex("systemctl daemon-reload") < 0 {
		t.Fatalf("socket not reloaded and restarted: %v", sim.h.Runner.Calls)
	}
	has := map[string]bool{}
	for _, a := range p.Verified {
		has[a] = true
	}
	if !has["127.0.0.1:2020"] || !has["[::1]:2020"] {
		t.Fatalf("both families must be verified: %v", p.Verified)
	}
	waitRollback(t, s)
	if sim.h.Exists("etc/systemd/system/ssh.socket.d/nimvor-port.conf") || !sim.listensOn(22) {
		t.Fatal("unconfirmed change left the socket on the new port")
	}
}

// IPv4-only host: no [::] listener is asked for (it would stop the socket from starting).
func TestSSHPortChangeSocketIPv4Only(t *testing.T) {
	fastConfirm(t)
	sim, s := newSSHSim(t)
	sim.socketActive = true
	if _, err := s.ConfigureSSH(context.Background(), sshWant(2020)); err != nil {
		t.Fatal(err)
	}
	body := sim.h.Read("etc/systemd/system/ssh.socket.d/nimvor-port.conf")
	if strings.Contains(body, "[::]") || strings.Contains(body, "BindIPv6Only") {
		t.Fatalf("IPv6 listener on an IPv4-only host:\n%s", body)
	}
	for _, a := range sim.probes {
		if strings.HasPrefix(a, "[::1]") {
			t.Fatal("probed IPv6 on an IPv4-only host")
		}
	}
	waitRollback(t, s)
}

// ssh.socket is enabled but stopped (sshd runs as a service now): the socket gets the port too,
// so the next boot does not come up on another one, but it is not restarted (port 22 is busy).
func TestSSHPortChangeSocketEnabledButStopped(t *testing.T) {
	fastConfirm(t)
	sim, s := newSSHSim(t)
	sim.socketEnabled = true
	if _, err := s.ConfigureSSH(context.Background(), sshWant(2020)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sim.h.Read("etc/systemd/system/ssh.socket.d/nimvor-port.conf"), "ListenStream=0.0.0.0:2020") {
		t.Fatal("the socket kept the old port")
	}
	if sim.callIndex("systemctl restart -- ssh.socket") >= 0 {
		t.Fatalf("a stopped socket must not be started: %v", sim.h.Runner.Calls)
	}
	if sim.callIndex("systemctl reload") < 0 {
		t.Fatal("the service was not reloaded")
	}
	waitRollback(t, s)
	if sim.h.Exists("etc/systemd/system/ssh.socket.d/nimvor-port.conf") {
		t.Fatal("socket drop-in not restored")
	}
}

// 2020 to another custom port, and back to 22.
func TestSSHPortChangeFromCustomPort(t *testing.T) {
	fastConfirm(t)
	for _, socket := range []bool{false, true} {
		t.Run(fmt.Sprintf("socket=%v", socket), func(t *testing.T) {
			sim, s := newSSHSim(t)
			sim.socketActive = socket
			for _, port := range []int{2020, 3030, 22} {
				p, err := s.ConfigureSSH(context.Background(), sshWant(port))
				if err != nil {
					t.Fatalf("to %d: %v", port, err)
				}
				if !sim.listensOn(port) || sim.listensOn(map[int]int{2020: 3030, 3030: 22, 22: 2020}[port]) {
					t.Fatalf("after moving to %d the model listens differently", port)
				}
				if err := s.guard.Confirm(p.ID); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// The incident: the socket restart does not bind the new port. The old code looked at
// `sshd -T` (which says 2020) and reported success; now the missing answer undoes everything.
func TestSSHPortChangeNotAnsweringIsRolledBack(t *testing.T) {
	fastConfirm(t)
	sim, s := newSSHSim(t)
	sim.socketActive = true
	sim.deaf = true
	sim.socketEnabled = true
	// The socket's own listing is not trusted either: it claims to listen on the right port.
	sim.h.Runner.Outputs["systemctl show --property=Listen --value -- ssh.socket"] = "[::]:2020 (Stream)\n0.0.0.0:2020 (Stream)\n"
	_, err := s.ConfigureSSH(context.Background(), sshWant(2020))
	if err == nil || !strings.Contains(err.Error(), "did not answer on port 2020") {
		t.Fatalf("expected a failure naming the port, got %v", err)
	}
	if s.guard.Current() != nil {
		t.Fatal("a failed change must not wait for confirmation")
	}
	if sim.h.Exists("etc/ssh/sshd_config.d/00-nimvor.conf") || sim.h.Exists("etc/systemd/system/ssh.socket.d/nimvor-port.conf") {
		t.Fatal("configuration left behind")
	}
	if sim.hasUFWPort(2020) {
		t.Fatalf("firewall rule left behind: %v", sim.ufw)
	}
	if n := countCalls(sim.h, "systemctl restart -- ssh.socket"); n < 2 {
		t.Fatalf("the socket must be restarted again to put the old port back (%d restarts)", n)
	}
}

// A plain sshd that does not come up on the new port: restarted once, then rolled back.
func TestSSHPortChangeServiceFailureRestartsOnceThenRollsBack(t *testing.T) {
	fastConfirm(t)
	sim, s := newSSHSim(t)
	sim.deaf = true
	SSHProbe = func(_ context.Context, addr string) error {
		if strings.HasSuffix(addr, ":22") {
			return nil
		}
		return fmt.Errorf("connection refused")
	}
	_, err := s.ConfigureSSH(context.Background(), sshWant(2020))
	if err == nil || !strings.Contains(err.Error(), "previous settings were restored") {
		t.Fatalf("got %v", err)
	}
	if countCalls(sim.h, "systemctl restart -- sshd.service") != 1 {
		t.Fatalf("expected one restart attempt: %v", sim.h.Runner.Calls)
	}
	if sim.sshdPort() != 22 || sim.hasUFWPort(2020) {
		t.Fatal("not rolled back")
	}
}

// When even the old port stays silent after the rollback, the message says to use the console.
func TestSSHPortChangeRollbackThatDoesNotRecover(t *testing.T) {
	fastConfirm(t)
	sim, s := newSSHSim(t)
	SSHProbe = func(context.Context, string) error { return fmt.Errorf("connection refused") }
	_, err := s.ConfigureSSH(context.Background(), sshWant(2020))
	if err == nil || !strings.Contains(err.Error(), "console") {
		t.Fatalf("got %v", err)
	}
	_ = sim
}

func TestSSHPortChangeSSHDTestFailureLeavesNothing(t *testing.T) {
	sim, s := newSSHSim(t)
	sim.h.Fail["sshd -t"] = "Bad configuration option"
	if _, err := s.ConfigureSSH(context.Background(), sshWant(2020)); err == nil {
		t.Fatal("invalid configuration accepted")
	}
	if sim.hasUFWPort(2020) || sim.h.Exists("etc/ssh/sshd_config.d/00-nimvor.conf") {
		t.Fatal("firewall rule or drop-in left behind")
	}
	if sim.callIndex("systemctl reload") >= 0 {
		t.Fatal("reloaded after a failed validation")
	}
}

func TestSSHPortChangeReloadFailureRestoresSettings(t *testing.T) {
	sim, s := newSSHSim(t)
	sim.h.Fail["systemctl reload"] = "Job for sshd.service failed"
	_, err := s.ConfigureSSH(context.Background(), sshWant(2020))
	if err == nil || !strings.Contains(err.Error(), "could not be reloaded") {
		t.Fatalf("got %v", err)
	}
	if sim.hasUFWPort(2020) || sim.sshdPort() != 22 {
		t.Fatal("not restored")
	}
}

// A port another program already listens on is refused before anything changes.
func TestSSHPortChangeRefusesPortInUse(t *testing.T) {
	sim, s := newSSHSim(t)
	os.MkdirAll(filepath.Join(sim.h.Base, "proc/net"), 0o755)
	os.WriteFile(filepath.Join(sim.h.Base, "proc/net/tcp"), []byte(
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
			"   0: 00000000:07E4 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0\n"), 0o644)
	_, err := s.ConfigureSSH(context.Background(), sshWant(2020))
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("got %v", err)
	}
	if sim.callIndex("ufw allow") >= 0 || sim.h.Exists("etc/ssh/sshd_config.d/00-nimvor.conf") {
		t.Fatal("something changed before the refusal")
	}
}

// nftables or iptables without UFW/firewalld: NimVor cannot open a port there, and says so before changing SSH.
func TestSSHPortChangeUnmanagedFirewall(t *testing.T) {
	const closed = "table inet filter {\n\tchain input {\n\t\ttype filter hook input priority filter; policy drop;\n\t\tiif \"lo\" accept\n\t\tct state established,related accept\n\t\ttcp dport 22 accept\n\t}\n}\n"
	const opened = "table inet filter {\n\tchain input {\n\t\ttype filter hook input priority filter; policy drop;\n\t\ttcp dport { 22, 2020 } accept\n\t}\n}\n"
	for name, tc := range map[string]struct {
		key, rules string
		wantErr    bool
	}{
		"nft closed":       {"nft list ruleset", closed, true},
		"nft opened":       {"nft list ruleset", opened, false},
		"iptables closed":  {"iptables -S INPUT", "-P INPUT DROP\n-A INPUT -p tcp -m tcp --dport 22 -j ACCEPT\n", true},
		"iptables opened":  {"iptables -S INPUT", "-P INPUT DROP\n-A INPUT -p tcp -m tcp --dport 2020 -j ACCEPT\n", false},
		"ip6tables closed": {"ip6tables -S INPUT", "-P INPUT DROP\n", true},
	} {
		t.Run(name, func(t *testing.T) {
			fastConfirm(t)
			sim, s := newSSHSim(t)
			sim.ufwOn = false
			sim.h.Runner.Outputs[tc.key] = tc.rules
			p, err := s.ConfigureSSH(context.Background(), sshWant(2020))
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "does not manage this firewall") {
					t.Fatalf("got %v", err)
				}
				if sim.h.Exists("etc/ssh/sshd_config.d/00-nimvor.conf") {
					t.Fatal("SSH was changed although the port is blocked")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			s.guard.Confirm(p.ID)
		})
	}
}

// NimVor's own tables have policy accept and must not be mistaken for a closed firewall.
func TestNftBlocksIgnoresAcceptPolicyAndOtherHooks(t *testing.T) {
	ruleset := "table inet nimvor_hulk {\n\tchain input {\n\t\ttype filter hook input priority -5; policy accept;\n\t\ttcp dport { 22 } ip saddr @login4 reject with tcp reset\n\t}\n}\ntable inet filter {\n\tchain forward {\n\t\ttype filter hook forward priority filter; policy drop;\n\t}\n}\n"
	if r := nftBlocks(ruleset, 2020); r != "" {
		t.Fatalf("false positive: %s", r)
	}
}

// The firewall says it added a rule but the list does not show it: SSH is not moved.
func TestSSHPortChangeFirewallRuleMissingAfterAdd(t *testing.T) {
	sim, s := newSSHSim(t)
	prev := sim.h.Respond
	sim.h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "ufw" && len(args) > 0 && args[0] == "allow" {
			return "Skipping adding existing rule", nil, true // exits 0 without adding it
		}
		return prev(id, args)
	}
	_, err := s.ConfigureSSH(context.Background(), sshWant(2020))
	if err == nil || !strings.Contains(err.Error(), "did not take a rule") {
		t.Fatalf("got %v", err)
	}
	if sim.h.Exists("etc/ssh/sshd_config.d/00-nimvor.conf") {
		t.Fatal("SSH was changed")
	}
}

func TestSocketDropInRendering(t *testing.T) {
	v4 := socketDropIn(2020, false)
	if v4 != "# Written by NimVor: the port SSH listens on (sshd is started by this socket).\n[Socket]\nListenStream=\nListenStream=0.0.0.0:2020\n" {
		t.Fatalf("%q", v4)
	}
	if v6 := socketDropIn(2020, true); !strings.Contains(v6, "ListenStream=[::]:2020\nBindIPv6Only=ipv6-only\n") {
		t.Fatalf("%q", v6)
	}
}

func TestReadListeners(t *testing.T) {
	sim, _ := newSSHSim(t)
	os.MkdirAll(filepath.Join(sim.h.Base, "proc/net"), 0o755)
	head := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	os.WriteFile(filepath.Join(sim.h.Base, "proc/net/tcp"), []byte(head+
		"   0: 00000000:0016 00000000:0000 0A 0:0 0:0 0 0 0 1\n"+ // 22 LISTEN
		"   1: 0100007F:1F90 0100007F:D000 01 0:0 0:0 0 0 0 2\n"), 0o644) // 8080 ESTABLISHED
	os.WriteFile(filepath.Join(sim.h.Base, "proc/net/tcp6"), []byte(head+
		"   0: 00000000000000000000000000000000:07E4 00000000000000000000000000000000:0000 0A 0:0 0:0 0 0 0 3\n"), 0o644) // 2020 LISTEN
	l := readListeners(sim.h.Env)
	if !l.known || !l.v4[22] || l.v4[8080] || !l.v6[2020] || l.v4[2020] {
		t.Fatalf("%+v", l)
	}
}

// SSHProbe against real sockets: an SSH greeting passes, other services and closed ports do not.
func TestSSHProbeRealSockets(t *testing.T) {
	serve := func(banner string) (string, func()) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Skip("no loopback: ", err)
		}
		go func() {
			for {
				c, err := ln.Accept()
				if err != nil {
					return
				}
				c.Write([]byte(banner))
				c.Close()
			}
		}()
		return ln.Addr().String(), func() { ln.Close() }
	}
	probe := func(addr string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// The package default, not the test stub.
		return realSSHProbe(ctx, addr)
	}
	ssh, stop1 := serve("SSH-2.0-OpenSSH_9.6\r\n")
	defer stop1()
	if err := probe(ssh); err != nil {
		t.Fatalf("SSH greeting rejected: %v", err)
	}
	web, stop2 := serve("HTTP/1.1 400 Bad Request\r\n\r\n")
	defer stop2()
	if err := probe(web); err == nil {
		t.Fatal("an HTTP server passed as SSH")
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().String()
	ln.Close()
	if err := probe(closed); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("closed port: %v", err)
	}
}

// Against a real OpenSSH: starts sshd on a free port with a throw-away key and
// requires verifyListening to see its greeting (skipped without sshd/root).
func TestVerifyListeningRealSSHD(t *testing.T) {
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		t.Skip("sshd not installed")
	}
	if os.Geteuid() != 0 {
		t.Skip("sshd needs root")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback")
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	dir := t.TempDir()
	key := filepath.Join(dir, "hk")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Skip("ssh-keygen: ", string(out))
	}
	conf := filepath.Join(dir, "sshd_config")
	os.WriteFile(conf, []byte(fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nPidFile %s\nUsePAM no\n", port, key, filepath.Join(dir, "pid"))), 0o600)
	os.MkdirAll("/run/sshd", 0o755)
	cmd := exec.Command(sshd, "-D", "-f", conf)
	if err := cmd.Start(); err != nil {
		t.Skip("sshd: ", err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	sim, _ := newSSHSim(t)
	SSHProbe = realSSHProbe
	verifyWait = 5 * time.Second
	got, err := verifyListening(context.Background(), sim.h.Env, port, false)
	if err != nil || len(got) != 1 || got[0] != fmt.Sprintf("127.0.0.1:%d", port) {
		t.Fatalf("real sshd not verified: %v %v", got, err)
	}
	verifyWait = 200 * time.Millisecond
	if _, err := verifyListening(context.Background(), sim.h.Env, port+1, false); err == nil {
		t.Fatal("a port nobody listens on was verified")
	}
}
