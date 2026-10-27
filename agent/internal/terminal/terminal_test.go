package terminal

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/ftpmgr"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// fixture: a website account "shop" with the given shell mode.
func fixture(t *testing.T, mode string) (*testhost.Host, *Manager) {
	t.Helper()
	h := testhost.New(t)
	f, _ := os.OpenFile(filepath.Join(h.Base, "etc/passwd"), os.O_APPEND|os.O_WRONLY, 0o644)
	fmt.Fprintf(f, "shop:x:%d:%d::/home/whacp/shop:/bin/bash\n", h.UID, h.GID)
	fmt.Fprintf(f, "other:x:%d:%d::/home/whacp/other:/bin/bash\n", h.UID+1, h.GID+1)
	f.Close()
	os.WriteFile(filepath.Join(h.Base, "etc/group"), []byte(fmt.Sprintf("shop:x:%d:\nwhacp-jail:x:990:shop,other\n", h.GID)), 0o644)
	os.MkdirAll(filepath.Join(h.Base, "home/whacp/shop"), 0o755)
	os.MkdirAll(filepath.Join(h.Base, "home/whacp/other"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "var/lib/whacp-agent/sftp.json"), []byte(`{"users":{"shop":{},"other":{}},"shells":{"shop":"`+mode+`","other":"normal"}}`), 0o600)
	os.WriteFile(filepath.Join(h.Base, "var/lib/whacp-agent/limits.json"), []byte(`{"accounts":{"acct7":{"users":["shop"]}}}`), 0o600)
	m := NewManager(h.Env)
	m.Enabled = true
	return h, m
}

func TestOpenChecksWhoAndWhat(t *testing.T) {
	_, m := fixture(t, "normal")
	m.Enabled = false
	if _, err := m.Open(OpenParams{User: "shop"}); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("disabled terminal: %v", err)
	}
	m.Enabled = true
	res, err := m.Open(OpenParams{User: "shop", Cols: 120, Rows: 40, ClientIP: "203.0.113.9", Label: "alice (#3)", Owner: strings.Repeat("ab", 16)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res["ticket"].(string)) != 64 || res["mode"] != "normal" {
		t.Fatalf("%v", res)
	}
	for name, p := range map[string]OpenParams{
		"root refused by default": {Root: true},
		"both":                    {User: "shop", Root: true},
		"neither":                 {},
		"system account":          {User: "www-data"},
		"unknown":                 {User: "nobody-here"},
		"bad label":               {User: "shop", Label: "<script>"},
		"bad owner":               {User: "shop", Owner: "xyz"},
		"bad address":             {User: "shop", ClientIP: "example"},
		"idle over max":           {User: "shop", IdleMinutes: 60, MaxMinutes: 30},
	} {
		if _, err := m.Open(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	m.Env.Cfg.TerminalRoot = true
	if res, err := m.Open(OpenParams{Root: true}); err != nil || res["mode"] != ModeRoot {
		t.Fatalf("root with terminal_root: %v %v", res, err)
	}

	_, off := fixture(t, "disabled")
	if _, err := off.Open(OpenParams{User: "shop"}); err == nil || !strings.Contains(err.Error(), "Shell access is disabled") {
		t.Fatalf("disabled shell: %v", err)
	}
	h, jail := fixture(t, "jailed")
	if _, err := jail.Open(OpenParams{User: "shop"}); err == nil {
		t.Fatal("jailed without the jail installed accepted")
	}
	os.MkdirAll(filepath.Join(h.Base, "usr/bin"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "usr/bin/bwrap"), []byte("x"), 0o755)
	os.MkdirAll(filepath.Join(h.Base, "usr/local/sbin"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "usr/local/sbin/whacp-jailsh"), []byte("x"), 0o755)
	if res, err := jail.Open(OpenParams{User: "shop"}); err != nil || res["mode"] != "jailed" {
		t.Fatalf("jailed: %v %v", res, err)
	}
}

func TestSystemdArgs(t *testing.T) {
	_, m := fixture(t, "normal")
	s, err := m.spec(OpenParams{User: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	s.TTY, s.Max = "/dev/pts/7", time.Hour
	a := strings.Join(SystemdArgs(s), "\n")
	for _, want := range []string{
		"--unit=whacp-term-" + s.ID + ".service", fmt.Sprintf("--uid=%d", s.UID), "--slice=whacp-acct7.slice",
		"--property=NoNewPrivileges=yes", "--property=CapabilityBoundingSet=", "--property=ProtectSystem=full",
		"--property=RuntimeMaxSec=3660", "--property=TasksMax=512", "--property=SupplementaryGroups=990",
		"--\n/bin/sh\n-c\nt=$$1; shift; exec \"$$@\" <\"$$t\" >\"$$t\" 2>&1\nwhacp-term\n/dev/pts/7\n/bin/bash\n-l",
	} {
		if !strings.Contains(a, want) {
			t.Errorf("missing %q in\n%s", want, a)
		}
	}
	if !strings.Contains(a, "--property=ProtectKernelTunables=yes") {
		t.Error("a normal shell lost ProtectKernelTunables")
	}
	// A jailed session leaves /proc's covers to its jail (it needs its own
	// /proc); everything else stays.
	s.Mode = ftpmgr.ShellJailed
	ja := strings.Join(SystemdArgs(s), "\n")
	if strings.Contains(ja, "ProtectKernelTunables") || strings.Contains(ja, "ProtectKernelLogs") {
		t.Errorf("jailed session keeps /proc covers:\n%s", ja)
	}
	for _, want := range []string{"--property=NoNewPrivileges=yes", "--property=CapabilityBoundingSet=", "--property=ProtectSystem=full", "--property=ProtectControlGroups=yes", "--slice=whacp-acct7.slice"} {
		if !strings.Contains(ja, want) {
			t.Errorf("jailed session lacks %q", want)
		}
	}
	root := Spec{ID: "0123456789abcdef", User: "root", Mode: ModeRoot, Program: "/bin/bash", Args: []string{"-l"}, Home: "/root", TTY: "/dev/pts/1", Max: time.Hour}
	ra := strings.Join(SystemdArgs(root), " ")
	if strings.Contains(ra, "--uid=") || strings.Contains(ra, "NoNewPrivileges") || strings.Contains(ra, "--slice") {
		t.Fatalf("root session: %s", ra)
	}
}

func TestKillAndStatusActions(t *testing.T) {
	_, m := fixture(t, "normal")
	r := actions.NewRegistry(true)
	RegisterWith(r, m)
	a, _ := r.Lookup("terminal.kill")
	if _, err := a.Handler(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("kill without id or owner accepted")
	}
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"id":"../x"}`)); err == nil {
		t.Fatal("bad id accepted")
	}
	st, _ := r.Lookup("terminal.status")
	res, err := st.Handler(context.Background(), nil)
	if err != nil || res.(map[string]any)["enabled"] != true || res.(map[string]any)["root_allowed"] != false {
		t.Fatalf("%v %v", res, err)
	}
}

// --- A WebSocket client for the end-to-end tests. ---

type client struct {
	c net.Conn
	r *bufio.Reader
}

func dial(t *testing.T, srv *httptest.Server, origin string) (*client, int) {
	t.Helper()
	c, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	host := srv.Listener.Addr().String()
	fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\nOrigin: %s\r\nX-Forwarded-For: 127.0.0.1\r\n\r\n", Path, host, strings.ReplaceAll(origin, "HOST", host))
	r := bufio.NewReader(c)
	resp, err := http.ReadResponse(r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode == 101 && resp.Header.Get("Sec-WebSocket-Accept") != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept key %q", resp.Header.Get("Sec-WebSocket-Accept"))
	}
	return &client{c: c, r: r}, resp.StatusCode
}

func (c *client) send(op byte, p []byte) {
	h := []byte{0x80 | op}
	switch n := len(p); {
	case n < 126:
		h = append(h, 0x80|byte(n))
	default:
		h = append(h, 0x80|126, byte(n>>8), byte(n))
	}
	var mask [4]byte
	rand.Read(mask[:])
	h = append(h, mask[:]...)
	for i, b := range p {
		h = append(h, b^mask[i%4])
	}
	c.c.Write(h)
}

func (c *client) read(t *testing.T) (byte, []byte) {
	t.Helper()
	c.c.SetReadDeadline(time.Now().Add(10 * time.Second))
	var h [2]byte
	if _, err := io.ReadFull(c.r, h[:]); err != nil {
		t.Fatalf("read: %v", err)
	}
	n := int(h[1] & 0x7F)
	if n == 126 {
		var b [2]byte
		io.ReadFull(c.r, b[:])
		n = int(binary.BigEndian.Uint16(b[:]))
	} else if n == 127 {
		var b [8]byte
		io.ReadFull(c.r, b[:])
		n = int(binary.BigEndian.Uint64(b[:]))
	}
	p := make([]byte, n)
	io.ReadFull(c.r, p)
	return h[0] & 0x0F, p
}

// until reads frames until a text message of type typ or output containing
// want arrives; it returns all output seen.
func (c *client) until(t *testing.T, typ, want string) (string, map[string]any) {
	t.Helper()
	var out strings.Builder
	for i := 0; i < 500; i++ {
		op, p := c.read(t)
		switch op {
		case opBinary:
			out.Write(p)
			if want != "" && strings.Contains(out.String(), want) {
				return out.String(), nil
			}
		case opText:
			var m map[string]any
			json.Unmarshal(p, &m)
			if m["type"] == typ {
				return out.String(), m
			}
		case opClose:
			t.Fatalf("closed early; output %q", out.String())
		}
	}
	t.Fatalf("not seen: %s %q in %q", typ, want, out.String())
	return "", nil
}

func e2e(t *testing.T) (*Manager, *httptest.Server) {
	if runtime.GOOS != "linux" {
		t.Skip("pseudo terminals: Linux only")
	}
	_, m := fixture(t, "normal")
	e2eSpawn(t, m)
	m.Verify = passwords.verify
	return m, serve(t, m)
}

// e2eSpawn runs sessions as /bin/sh of the test's own user.
func e2eSpawn(t *testing.T, m *Manager) {
	home := t.TempDir()
	var mu sync.Mutex
	m.Spawn = func(ctx context.Context, s Spec) (func(), error) {
		mu.Lock()
		defer mu.Unlock()
		s.Program, s.Home = "/bin/sh", home
		return simulationSpawn(ctx, s)
	}
}

func serve(t *testing.T, m *Manager) *httptest.Server {
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)
	return srv
}

// passwords stands in for /etc/shadow (the real check, unix_chkpwd, is
// covered by the real-host tests).
var passwords = &fakeShadow{pw: map[string]string{"shop": "shop-pass", "other": "other-pass"}}

type fakeShadow struct {
	mu sync.Mutex
	pw map[string]string
}

func (f *fakeShadow) verify(_ context.Context, user, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.pw[user]; !ok || password == "" || p != password {
		return errWrongPassword
	}
	return nil
}

func (f *fakeShadow) set(user, password string) {
	f.mu.Lock()
	f.pw[user] = password
	f.mu.Unlock()
}

func authMsg(ticket, password string) []byte {
	b, _ := json.Marshal(map[string]string{"type": "auth", "ticket": ticket, "password": password})
	return b
}

func TestMain(m *testing.M) {
	failurePause = 10 * time.Millisecond
	os.Exit(m.Run())
}

func newTicket(t *testing.T, m *Manager, p OpenParams) string {
	t.Helper()
	res, err := m.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return res["ticket"].(string)
}

func TestSessionEndToEnd(t *testing.T) {
	m, srv := e2e(t)
	owner := strings.Repeat("cd", 16)
	tk := newTicket(t, m, OpenParams{User: "shop", ClientIP: "127.0.0.1", Label: "alice", Owner: owner})
	c, code := dial(t, srv, "https://HOST")
	if code != 101 {
		t.Fatalf("handshake %d", code)
	}
	c.send(opText, (authMsg(tk, "shop-pass")))
	_, ready := c.until(t, "ready", "")
	if ready["user"] != "shop" || ready["mode"] != "normal" {
		t.Fatalf("%v", ready)
	}
	c.send(opText, []byte(`{"type":"resize","cols":132,"rows":43}`))
	c.send(opBinary, []byte("echo sum-$((6*7)); stty size\n"))
	out, _ := c.until(t, "", "43 132")
	if !strings.Contains(out, "sum-42") {
		t.Fatalf("output %q", out)
	}
	list := m.List()
	if len(list) != 1 || list[0]["label"] != "alice" || list[0]["bytes_in"].(int64) == 0 {
		t.Fatalf("%v", list)
	}
	// Signing out of the panel ends the sign-in's sessions.
	if n := m.Kill("", owner, "Closed: you signed out of the panel."); n != 1 {
		t.Fatalf("killed %d", n)
	}
	_, exit := c.until(t, "exit", "")
	if !strings.Contains(exit["reason"].(string), "signed out") || len(m.List()) != 0 {
		t.Fatalf("%v %v", exit, m.List())
	}

	// The ticket was used up.
	c2, _ := dial(t, srv, "https://HOST")
	c2.send(opText, (authMsg(tk, "shop-pass")))
	if _, e := c2.until(t, "exit", ""); !strings.Contains(e["reason"].(string), "no longer valid") {
		t.Fatalf("%v", e)
	}
}

func TestShellExitEndsSession(t *testing.T) {
	m, srv := e2e(t)
	c, _ := dial(t, srv, "http://HOST")
	c.send(opText, (authMsg(newTicket(t, m, OpenParams{User: "shop"}), "shop-pass")))
	c.until(t, "ready", "")
	c.send(opBinary, []byte("exit\n"))
	if _, e := c.until(t, "exit", ""); e["reason"] != "The shell exited." {
		t.Fatalf("%v", e)
	}
}

func TestIdleTimeout(t *testing.T) {
	m, srv := e2e(t)
	m.CheckEvery = 50 * time.Millisecond
	var mu sync.Mutex
	now := time.Now()
	m.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	c, _ := dial(t, srv, "http://HOST")
	c.send(opText, (authMsg(newTicket(t, m, OpenParams{User: "shop", IdleMinutes: 5}), "shop-pass")))
	c.until(t, "ready", "")
	mu.Lock()
	now = now.Add(6 * time.Minute)
	mu.Unlock()
	if _, e := c.until(t, "exit", ""); !strings.Contains(e["reason"].(string), "5 minutes without input") {
		t.Fatalf("%v", e)
	}
}

func TestRefusals(t *testing.T) {
	m, srv := e2e(t)
	// Another site's page cannot open the WebSocket.
	if _, code := dial(t, srv, "https://evil.example"); code != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", code)
	}
	// A ticket is bound to the address the panel saw.
	tk := newTicket(t, m, OpenParams{User: "shop", ClientIP: "198.51.100.4"})
	c, _ := dial(t, srv, "https://HOST")
	c.send(opText, (authMsg(tk, "shop-pass")))
	c.until(t, "exit", "")
	// Expired.
	tk = newTicket(t, m, OpenParams{User: "shop"})
	m.Now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	c, _ = dial(t, srv, "https://HOST")
	c.send(opText, (authMsg(tk, "shop-pass")))
	c.until(t, "exit", "")
	m.Now = time.Now
	// No authentication message.
	c, _ = dial(t, srv, "https://HOST")
	c.send(opBinary, []byte("ls\n"))
	if op, _ := c.read(t); op != opClose {
		t.Fatalf("op %d", op)
	}
	// Unmasked frames are a protocol error.
	c, _ = dial(t, srv, "https://HOST")
	c.c.Write([]byte{0x81, 0x02, 'h', 'i'})
	if op, _ := c.read(t); op != opClose {
		t.Fatalf("op %d", op)
	}
	if len(m.List()) != 0 {
		t.Fatal("sessions left")
	}
}

func TestCapacity(t *testing.T) {
	m, srv := e2e(t)
	var cs []*client
	for i := 0; i < maxPerAccount; i++ {
		c, _ := dial(t, srv, "https://HOST")
		c.send(opText, (authMsg(newTicket(t, m, OpenParams{User: "shop"}), "shop-pass")))
		c.until(t, "ready", "")
		cs = append(cs, c)
	}
	if _, err := m.Open(OpenParams{User: "shop"}); err == nil || !strings.Contains(err.Error(), "already has 3") {
		t.Fatalf("fourth: %v", err)
	}
	m.CloseAll("bye")
	for _, c := range cs {
		c.until(t, "exit", "")
	}
}
