package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/ftpmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

// syncBuf is a log sink safe for the session goroutines.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func logTo(m *Manager) *syncBuf {
	buf := &syncBuf{}
	m.Env.Log = slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return buf
}

func passwordMsg(pw string) []byte {
	b, _ := json.Marshal(map[string]string{"type": "password", "password": pw})
	return b
}

// The account's Linux password opens the terminal; a wrong one is refused,
// with two more tries on the same connection.
func TestLinuxPasswordIsRequired(t *testing.T) {
	m, srv := e2e(t)
	c, _ := dial(t, srv, "https://HOST")
	c.send(opText, authMsg(newTicket(t, m, OpenParams{User: "shop"}), "not-it"))
	_, f := c.until(t, "auth_failed", "")
	if f["attempts_left"].(float64) != 2 || !strings.Contains(f["reason"].(string), "shop") {
		t.Fatalf("%v", f)
	}
	if len(m.List()) != 0 {
		t.Fatal("a session started before the password was right")
	}
	c.send(opText, passwordMsg("shop-pass"))
	if _, r := c.until(t, "ready", ""); r["user"] != "shop" {
		t.Fatalf("%v", r)
	}

	// No password at all, and another account's password, are refused.
	for _, pw := range []string{"", "other-pass"} {
		c, _ := dial(t, srv, "https://HOST")
		c.send(opText, authMsg(newTicket(t, m, OpenParams{User: "shop"}), pw))
		c.until(t, "auth_failed", "")
	}
	m.guard.clear("user:shop", "ip:127.0.0.1")

	// Three wrong passwords end the connection.
	c, _ = dial(t, srv, "https://HOST")
	c.send(opText, authMsg(newTicket(t, m, OpenParams{User: "shop"}), "a"))
	c.until(t, "auth_failed", "")
	c.send(opText, passwordMsg("b"))
	c.until(t, "auth_failed", "")
	c.send(opText, passwordMsg("c"))
	if _, e := c.until(t, "exit", ""); !strings.Contains(e["reason"].(string), "Wrong password") {
		t.Fatalf("%v", e)
	}
}

// Repeated wrong passwords block the account (and the address) for a
// while, even with the right password.
func TestBruteForceIsThrottled(t *testing.T) {
	m, srv := e2e(t)
	for i := 0; i < maxFailures; i++ {
		c, _ := dial(t, srv, "https://HOST")
		c.send(opText, authMsg(newTicket(t, m, OpenParams{User: "shop"}), "guess"))
		c.until(t, "auth_failed", "")
		c.c.Close()
	}
	c, _ := dial(t, srv, "https://HOST")
	c.send(opText, authMsg(newTicket(t, m, OpenParams{User: "shop"}), "shop-pass"))
	if _, e := c.until(t, "exit", ""); !strings.Contains(e["reason"].(string), "Too many wrong passwords") {
		t.Fatalf("%v", e)
	}
	if len(m.List()) != 0 {
		t.Fatal("a blocked account got a session")
	}
}

// Changing the password (or taking shell access away) ends the account's
// sessions; the old password no longer opens one, the new one does.
func TestPasswordChangeEndsSessions(t *testing.T) {
	h, m := fixture(t, "normal")
	r := actions.NewRegistry(true)
	Register(r, h.Env) // wires ftpmgr.CredentialsChanged to Shared
	t.Cleanup(func() { ftpmgr.CredentialsChanged = nil })
	sm := Shared
	sm.Enabled, sm.Spawn, sm.Verify = true, m.Spawn, passwords.verify
	e2eSpawn(t, sm)
	defer passwords.set("shop", "shop-pass")
	srv := serve(t, sm)

	c, _ := dial(t, srv, "https://HOST")
	c.send(opText, authMsg(newTicket(t, sm, OpenParams{User: "shop"}), "shop-pass"))
	c.until(t, "ready", "")
	other, _ := dial(t, srv, "https://HOST")
	other.send(opText, authMsg(newTicket(t, sm, OpenParams{User: "other"}), "other-pass"))
	other.until(t, "ready", "")

	passwords.set("shop", "brand-new-pass")
	ftpmgr.CredentialsChanged("shop") // what sftp.configure calls after chpasswd
	if _, e := c.until(t, "exit", ""); !strings.Contains(e["reason"].(string), "password") {
		t.Fatalf("%v", e)
	}
	if l := sm.List(); len(l) != 1 || l[0]["user"] != "other" {
		t.Fatalf("other account's session affected: %v", l)
	}
	c, _ = dial(t, srv, "https://HOST")
	c.send(opText, authMsg(newTicket(t, sm, OpenParams{User: "shop"}), "shop-pass"))
	c.until(t, "auth_failed", "")
	c.send(opText, passwordMsg("brand-new-pass"))
	c.until(t, "ready", "")
	sm.CloseAll("done")
}

// Passwords never reach the agent's log, neither right nor wrong ones.
func TestPasswordsAreNotLogged(t *testing.T) {
	m, srv := e2e(t)
	buf := logTo(m)
	passwords.set("shop", "Sup3r-Secret-Linux-Pw")
	defer passwords.set("shop", "shop-pass")
	c, _ := dial(t, srv, "https://HOST")
	c.send(opText, authMsg(newTicket(t, m, OpenParams{User: "shop", ClientIP: "127.0.0.1"}), "Wr0ng-Guess-Pw"))
	c.until(t, "auth_failed", "")
	c.send(opText, passwordMsg("Sup3r-Secret-Linux-Pw"))
	c.until(t, "ready", "")
	m.CloseAll("done")
	c.until(t, "exit", "")
	log := buf.String()
	for _, secret := range []string{"Sup3r-Secret-Linux-Pw", "Wr0ng-Guess-Pw"} {
		if strings.Contains(log, secret) {
			t.Fatalf("a password is in the log:\n%s", log)
		}
	}
	for _, want := range []string{"wrong account password", "account password accepted", "user=shop", "terminal session ended"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// Two accounts' sessions at once: each sees only its own output.
func TestSessionsAreIsolated(t *testing.T) {
	m, srv := e2e(t)
	a, _ := dial(t, srv, "https://HOST")
	a.send(opText, authMsg(newTicket(t, m, OpenParams{User: "shop"}), "shop-pass"))
	a.until(t, "ready", "")
	b, _ := dial(t, srv, "https://HOST")
	b.send(opText, authMsg(newTicket(t, m, OpenParams{User: "other"}), "other-pass"))
	b.until(t, "ready", "")
	a.send(opBinary, []byte("echo MARK-A-$((1+1))\n"))
	b.send(opBinary, []byte("echo MARK-B-$((2+2))\n"))
	outA, _ := a.until(t, "", "MARK-A-2")
	outB, _ := b.until(t, "", "MARK-B-4")
	if strings.Contains(outA, "MARK-B") || strings.Contains(outB, "MARK-A") {
		t.Fatalf("output crossed sessions:\nA: %q\nB: %q", outA, outB)
	}
	// Ending one account's sessions leaves the other's.
	if n := m.KillUser("shop", "bye"); n != 1 {
		t.Fatalf("killed %d", n)
	}
	a.until(t, "exit", "")
	if l := m.List(); len(l) != 1 || l[0]["user"] != "other" {
		t.Fatalf("%v", l)
	}
	m.CloseAll("done")
}

// A website account in a privileged group gets no terminal.
func TestPrivilegedGroupsAreRefused(t *testing.T) {
	for _, g := range []string{"sudo:x:27:shop", "docker:x:998:x,shop", "adm:x:4:shop", "shadow:x:42:shop", "systemd-journal:x:101:shop", "lxd:x:110:shop", "disk:x:6:shop"} {
		h, m := fixture(t, "normal")
		f, _ := os.OpenFile(filepath.Join(h.Base, "etc/group"), os.O_APPEND|os.O_WRONLY, 0o644)
		f.WriteString(g + "\n")
		f.Close()
		if _, err := m.Open(OpenParams{User: "shop"}); err == nil || !strings.Contains(err.Error(), "privileged groups") {
			t.Errorf("%s: %v", g, err)
		}
	}
	// whacp-jail (the jailed shell's group) is fine.
	_, m := fixture(t, "normal")
	if _, err := m.Open(OpenParams{User: "shop"}); err != nil {
		t.Fatal(err)
	}
}

// The real check runs PAM's unix_chkpwd with the password on standard
// input only, then checks that the account has not expired.
func TestCheckPasswordUsesUnixChkpwd(t *testing.T) {
	h := testhost.New(t)
	fail := map[string]bool{}
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "unix_chkpwd" && fail[args[1]] {
			return "", os.ErrPermission, true
		}
		return "", nil, false
	}
	if err := checkPassword(context.Background(), h.Env, "shop", "Pw-From-Browser-1"); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.Runner.Calls {
		if strings.Contains(c, "Pw-From-Browser-1") {
			t.Fatalf("password in the command line: %q", c)
		}
	}
	if !strings.Contains(strings.Join(h.Runner.Stdins, "|"), "Pw-From-Browser-1\x00") {
		t.Fatalf("password not on stdin: %q", h.Runner.Stdins)
	}
	want := []string{"unix_chkpwd shop nonull", "unix_chkpwd shop chkexpiry"}
	if strings.Join(h.Runner.Calls[len(h.Runner.Calls)-2:], "|") != strings.Join(want, "|") {
		t.Fatalf("calls %q", h.Runner.Calls)
	}
	fail["nonull"] = true
	if err := checkPassword(context.Background(), h.Env, "shop", "wrong"); err != errWrongPassword {
		t.Fatalf("wrong: %v", err)
	}
	fail["nonull"], fail["chkexpiry"] = false, true
	if err := checkPassword(context.Background(), h.Env, "shop", "Pw-From-Browser-1"); err != errWrongPassword {
		t.Fatalf("expired: %v", err)
	}
	for _, bad := range []string{"", strings.Repeat("x", maxPasswordSz+1), "a\x00b"} {
		if checkPassword(context.Background(), h.Env, "shop", bad) != errWrongPassword {
			t.Errorf("%q accepted", bad)
		}
	}
}
