//go:build realhost

package mailmgr

import (
	"bufio"
	"context"
	cryptotls "crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// Requires postfix, dovecot-imapd, dovecot-lmtpd and opendkim installed,
// and systemd (or a stand-in) to restart them.
func TestRealHostMail(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	for _, bin := range []string{"/usr/sbin/postfix", "/usr/sbin/dovecot", "/usr/sbin/opendkim", "/usr/lib/dovecot/lmtp"} {
		if _, err := os.Stat(bin); err != nil {
			t.Skip("needs the mail packages")
		}
	}
	root := filesystem.Host()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(60*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)

	// Keep the package files to put them back.
	saved := map[string][]byte{}
	for _, p := range []string{"/etc/postfix/main.cf", "/etc/postfix/master.cf", env.M.DovecotAuthConf} {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		saved[p] = b
	}
	t.Cleanup(func() {
		for p, b := range saved {
			os.WriteFile(p, b, 0o644)
		}
		os.Remove(env.M.DovecotDropIn)
		exec.Command("systemctl", "stop", DKIMUnit).Run()
		os.Remove(filepath.Join(env.M.UnitDir, DKIMUnit))
		os.RemoveAll(env.M.MailDir)
		os.RemoveAll(filepath.Join(env.M.VmailDir, "rt-mail.test"))
		exec.Command("systemctl", "daemon-reload").Run()
		exec.Command("systemctl", "restart", "dovecot").Run()
		exec.Command("systemctl", "restart", "postfix").Run()
	})
	call := func(name string, params any) (map[string]any, error) {
		a, _ := r.Lookup(name)
		b, _ := json.Marshal(params)
		res, err := a.Handler(context.Background(), b)
		if err != nil {
			return nil, err
		}
		out, _ := json.Marshal(res)
		var m map[string]any
		json.Unmarshal(out, &m)
		return m, nil
	}
	must := func(name string, params any) map[string]any {
		t.Helper()
		m, err := call(name, params)
		if err != nil {
			if ae, ok := err.(*actions.Error); ok {
				t.Fatalf("%s: %s\n%s", name, ae.Message, ae.Details)
			}
			t.Fatalf("%s: %+v", name, err)
		}
		return m
	}
	if _, err := call("mail.configure", map[string]any{"hostname": "mail.whacp-rt.test"}); err == nil {
		t.Fatal("set up without confirmation")
	}
	must("mail.configure", map[string]any{"hostname": "mail.whacp-rt.test", "confirm_edit_package_config": true})
	if b, _ := os.ReadFile(env.M.DovecotAuthConf); !strings.Contains(string(b), "#!include auth-system.conf.ext") {
		t.Fatal("system-account login not disabled")
	}
	dom := must("mail.domain_add", map[string]any{"domain": "rt-mail.test"})
	if !strings.HasPrefix(dom["dkim_txt"].(string), "v=DKIM1; k=rsa; p=") {
		t.Fatalf("domain = %v", dom)
	}
	must("mail.mailbox_save", map[string]any{"address": "Alice@rt-mail.test", "password": "Alice-mail-pass-1", "quota_mb": 100})
	must("mail.mailbox_save", map[string]any{"address": "bob@rt-mail.test", "password": "Bob-mail-pass-12"})
	must("mail.alias_save", map[string]any{"source": "info@rt-mail.test", "destinations": []string{"alice@rt-mail.test"}})

	maildir := filepath.Join(env.M.VmailDir, "rt-mail.test/alice/Maildir")
	waitMail := func(n int) []string {
		t.Helper()
		for i := 0; i < 60; i++ {
			files, _ := filepath.Glob(filepath.Join(maildir, "new", "*"))
			cur, _ := filepath.Glob(filepath.Join(maildir, "cur", "*"))
			files = append(files, cur...)
			if len(files) >= n {
				return files
			}
			time.Sleep(250 * time.Millisecond)
		}
		out, _ := exec.Command("postqueue", "-p").CombinedOutput()
		t.Fatalf("mail not delivered to %s; queue:\n%s", maildir, out)
		return nil
	}

	// Inbound: to the alias, delivered to alice.
	inbound := func(to, body string) error {
		c, err := smtp.Dial("127.0.0.1:25")
		if err != nil {
			return err
		}
		defer c.Close()
		if err := c.Hello("sender.example"); err != nil {
			return err
		}
		if err := c.Mail("sender@example.net"); err != nil {
			return err
		}
		if err := c.Rcpt(to); err != nil {
			return err
		}
		w, err := c.Data()
		if err != nil {
			return err
		}
		io.WriteString(w, "Subject: hello\r\n\r\n"+body+"\r\n")
		if err := w.Close(); err != nil {
			return err
		}
		return c.Quit()
	}
	if err := inbound("info@rt-mail.test", "inbound test"); err != nil {
		t.Fatalf("inbound: %v", err)
	}
	if err := inbound("nobody@rt-mail.test", "x"); err == nil {
		t.Fatal("mail for an unknown mailbox accepted")
	}
	files := waitMail(1)
	if b, _ := os.ReadFile(files[0]); !strings.Contains(string(b), "inbound test") {
		t.Fatalf("message = %s", b)
	}

	// No open relay on port 25 for clients outside this machine (simulated
	// by trusting another address only).
	exec.Command("postconf", "-e", "mynetworks=127.0.0.2/32").Run()
	exec.Command("postfix", "reload").Run()
	time.Sleep(time.Second)
	c, err := smtp.Dial("127.0.0.1:25")
	if err != nil {
		t.Fatal(err)
	}
	c.Hello("test.example")
	c.Mail("sender@example.net")
	if err := c.Rcpt("someone@example.org"); err == nil {
		t.Fatal("port 25 relays to other domains")
	}
	c.Close()
	exec.Command("postconf", "-e", "mynetworks=127.0.0.0/8").Run()
	exec.Command("postfix", "reload").Run()

	// IMAP over TLS with the mailbox password; wrong password refused.
	imap := func(user, pass string) string {
		conn, err := cryptotls.Dial("tcp", "127.0.0.1:993", &cryptotls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(10 * time.Second))
		rd := bufio.NewReader(conn)
		rd.ReadString('\n')
		fmt.Fprintf(conn, "a1 LOGIN %s %s\r\na2 SELECT INBOX\r\na3 LOGOUT\r\n", user, pass)
		var sb strings.Builder
		for {
			line, err := rd.ReadString('\n')
			sb.WriteString(line)
			if err != nil || strings.HasPrefix(line, "a3 ") || strings.HasPrefix(line, "a1 NO") {
				break
			}
		}
		return sb.String()
	}
	if out := imap("alice@rt-mail.test", "Alice-mail-pass-1"); !strings.Contains(out, "a1 OK") || !strings.Contains(out, "1 EXISTS") {
		t.Fatalf("IMAP login:\n%s", out)
	}
	if out := imap("alice@rt-mail.test", "wrong-password"); !strings.Contains(out, "a1 NO") {
		t.Fatalf("wrong password accepted:\n%s", out)
	}

	// Submission: TLS + authentication required, sender must be the login,
	// outgoing mail is DKIM-signed.
	submit := func(from, user, pass string) error {
		c, err := smtp.Dial("127.0.0.1:587")
		if err != nil {
			return err
		}
		defer c.Close()
		if err := c.Hello("client.example"); err != nil {
			return err
		}
		if err := c.StartTLS(&cryptotls.Config{InsecureSkipVerify: true, ServerName: "localhost"}); err != nil {
			return err
		}
		if err := c.Auth(smtp.PlainAuth("", user, pass, "127.0.0.1")); err != nil {
			return err
		}
		if err := c.Mail(from); err != nil {
			return err
		}
		if err := c.Rcpt("alice@rt-mail.test"); err != nil {
			return err
		}
		w, err := c.Data()
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "From: %s\r\nTo: alice@rt-mail.test\r\nSubject: signed\r\n\r\nsubmitted by bob\r\n", from)
		if err := w.Close(); err != nil {
			return err
		}
		return c.Quit()
	}
	if err := submit("bob@rt-mail.test", "bob@rt-mail.test", "wrong-password"); err == nil {
		t.Fatal("submission with a wrong password accepted")
	}
	if err := submit("alice@rt-mail.test", "bob@rt-mail.test", "Bob-mail-pass-12"); err == nil {
		t.Fatal("bob could send as alice")
	}
	if err := submit("bob@rt-mail.test", "bob@rt-mail.test", "Bob-mail-pass-12"); err != nil {
		t.Fatalf("submission: %v", err)
	}
	files = waitMail(2)
	found := false
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), "submitted by bob") {
			found = true
			if !strings.Contains(string(b), "DKIM-Signature:") || !strings.Contains(string(b), "d=rt-mail.test") {
				t.Fatalf("not DKIM-signed:\n%s", b)
			}
		}
	}
	if !found {
		t.Fatal("submitted message not delivered")
	}
	// Plain port 25 does not offer authentication.
	c, _ = smtp.Dial("127.0.0.1:25")
	c.Hello("client.example")
	if ok, _ := c.Extension("AUTH"); ok {
		t.Fatal("port 25 offers AUTH")
	}
	c.Close()

	// Removing: the domain needs its mailboxes and aliases gone first.
	if _, err := call("mail.domain_remove", map[string]any{"domain": "rt-mail.test", "confirm": "rt-mail.test"}); err == nil {
		t.Fatal("domain with mailboxes removed")
	}
	must("mail.alias_delete", map[string]any{"source": "info@rt-mail.test"})
	must("mail.mailbox_delete", map[string]any{"address": "alice@rt-mail.test", "confirm": "alice@rt-mail.test"})
	must("mail.mailbox_delete", map[string]any{"address": "bob@rt-mail.test", "confirm": "bob@rt-mail.test"})
	if _, err := os.Stat(maildir); err == nil {
		t.Fatal("deleted mailbox's mail left in place")
	}
	must("mail.domain_remove", map[string]any{"domain": "rt-mail.test", "confirm": "rt-mail.test"})
	if out := imap("alice@rt-mail.test", "Alice-mail-pass-1"); !strings.Contains(out, "a1 NO") {
		t.Fatalf("deleted mailbox can still sign in:\n%s", out)
	}
}
