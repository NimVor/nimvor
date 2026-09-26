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
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/smtp"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	for _, bins := range [][]string{{"/usr/sbin/postfix"}, {"/usr/sbin/dovecot"}, {"/usr/sbin/opendkim"}, {"/usr/lib/dovecot/lmtp", "/usr/libexec/dovecot/lmtp"}} {
		found := false
		for _, bin := range bins {
			_, err := os.Stat(bin)
			found = found || err == nil
		}
		if !found {
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
		tree, _ := exec.Command("find", env.M.VmailDir, "-maxdepth", "6").CombinedOutput()
		t.Fatalf("mail not delivered to %s; queue:\n%s\nstorage:\n%s", maildir, out, tree)
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
	testSendmailShim(t, env, must)
	// Mail the spam filter flagged goes to Junk (Sieve).
	c0, err := smtp.Dial("127.0.0.1:25")
	if err != nil {
		t.Fatal(err)
	}
	c0.Hello("sender.example")
	c0.Mail("spammer@example.net")
	c0.Rcpt("alice@rt-mail.test")
	w0, _ := c0.Data()
	io.WriteString(w0, "Subject: cheap\r\nX-Spam: Yes\r\n\r\nspam body\r\n")
	w0.Close()
	c0.Quit()
	junk := filepath.Join(env.M.VmailDir, "rt-mail.test/alice/Maildir/.Junk")
	for i := 0; i < 60; i++ {
		if files, _ := filepath.Glob(filepath.Join(junk, "*", "*")); len(files) > 0 {
			break
		}
		if i == 59 {
			t.Fatal("flagged message not filed into Junk")
		}
		time.Sleep(250 * time.Millisecond)
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
		diag := ""
		for _, c := range [][]string{
			{"sh", "-c", "journalctl --no-pager -o cat -u postfix | grep -iE 'warn|fatal|error|tls|key|cert' | tail -20"},
			{"sh", "-c", "postconf -n | grep -i tls; postconf -M submission/inet"},
			{"sh", "-c", "ls -lZ /etc/whacp-mail/ | head -20"},
		} {
			out, _ := exec.Command(c[0], c[1:]...).CombinedOutput()
			diag += string(out) + "\n"
		}
		t.Fatalf("submission: %v\n%s", err, diag)
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

	// Spam filter: GTUBE is refused while connected; mail above the marking
	// score (set low here) is filed into Junk; off again restores Postfix.
	if _, err := os.Stat("/usr/bin/rspamd"); err == nil {
		t.Cleanup(func() {
			for _, f := range []string{"actions.conf", "worker-proxy.inc", "milter_headers.conf", "dkim_signing.conf"} {
				os.Remove("/etc/rspamd/local.d/" + f)
			}
			exec.Command("systemctl", "restart", "rspamd").Run()
		})
		must("mail.spam_configure", map[string]any{"enabled": true, "add_header": 1, "reject": 15, "confirm_replace": true})
		if out, _ := exec.Command("postconf", "-h", "smtpd_milters").CombinedOutput(); !strings.Contains(string(out), "127.0.0.1:11332") {
			t.Fatalf("milters = %s", out)
		}
		gtube := "XJS*C4JDBQADN1.NSBN3*2IDNEN*GTUBE-STANDARD-ANTI-UBE-TEST-EMAIL*C.34X"
		if err := inbound("bob@rt-mail.test", gtube); err == nil || !strings.Contains(err.Error(), "5") {
			t.Fatalf("GTUBE accepted: %v", err)
		}
		before, _ := filepath.Glob(filepath.Join(junk, "*", "*"))
		if err := inbound("alice@rt-mail.test", "probably spam"); err != nil {
			t.Fatalf("marked mail: %v", err)
		}
		for i := 0; i < 60; i++ {
			if after, _ := filepath.Glob(filepath.Join(junk, "*", "*")); len(after) > len(before) {
				break
			}
			if i == 59 {
				t.Fatal("mail above the marking score not filed into Junk")
			}
			time.Sleep(250 * time.Millisecond)
		}
		must("mail.spam_configure", map[string]any{"enabled": false, "add_header": 6, "reject": 15})
		if out, _ := exec.Command("postconf", "-h", "smtpd_milters").CombinedOutput(); strings.Contains(string(out), "11332") {
			t.Fatalf("milters after turning off = %s", out)
		}
	}

	// Webmail: Roundcube signs in with the mailbox password and sends
	// through submission (as the signed-in user).
	if _, err := os.Stat(env.M.RoundcubePublic + "/index.php"); err == nil && env.Runner.Available("nginx") && os.Getenv("WHACP_TEST_WEBMAIL_UNSUPPORTED") == "1" {
		// Ubuntu 26.04 without a second PHP: the distribution's Roundcube
		// does not run on its PHP 8.5. WhaCP must refuse clearly and leave
		// nothing behind.
		_, err := call("mail.webmail_configure", map[string]any{"enabled": true, "hostname": "webmail.whacp-rt.test"})
		if err == nil || !strings.Contains(err.Error(), "does not work with PHP") {
			t.Fatalf("webmail with an incompatible PHP: %v", err)
		}
		if _, err := os.Stat(filepath.Join(env.M.NginxEnabled, "whacp-webmail.conf")); err == nil {
			t.Error("the webmail site was left enabled after the refusal")
		}
		t.Log("webmail: refused on this system's PHP, as expected (known limitation)")
	} else if _, err := os.Stat(env.M.RoundcubePublic + "/index.php"); err == nil && env.Runner.Available("nginx") {
		testWebmail(t, env, must, waitMail)
		t.Log("webmail: signed in and sent through Roundcube")
	} else if os.Getenv("CI") != "" {
		t.Fatal("CI installs Roundcube and Nginx for the webmail test")
	}

	// Removing: the domain needs its mailboxes and aliases gone first.
	if _, err := call("mail.domain_remove", map[string]any{"domain": "rt-mail.test", "confirm": "rt-mail.test"}); err == nil {
		t.Fatal("domain with mailboxes removed")
	}
	must("mail.alias_delete", map[string]any{"source": "info@rt-mail.test"})
	del := must("mail.mailbox_delete", map[string]any{"address": "alice@rt-mail.test", "confirm": "alice@rt-mail.test"})
	must("mail.mailbox_delete", map[string]any{"address": "bob@rt-mail.test", "confirm": "bob@rt-mail.test"})
	if _, err := os.Stat(maildir); err == nil {
		tree, _ := exec.Command("ls", "-laZ", filepath.Dir(maildir), filepath.Dir(filepath.Dir(maildir))).CombinedOutput()
		t.Fatalf("deleted mailbox's mail left in place: %v\n%s", del, tree)
	}
	must("mail.domain_remove", map[string]any{"domain": "rt-mail.test", "confirm": "rt-mail.test"})
	if out := imap("alice@rt-mail.test", "Alice-mail-pass-1"); !strings.Contains(out, "a1 NO") {
		t.Fatalf("deleted mailbox can still sign in:\n%s", out)
	}
}

func testWebmail(t *testing.T, env *host.Env, must func(string, any) map[string]any, waitMail func(int) []string) {
	t.Helper()
	const hostname = "webmail.whacp-rt.test"
	t.Cleanup(func() {
		w := env.M
		os.Remove(filepath.Join(w.NginxEnabled, "whacp-webmail.conf"))
		os.Remove(filepath.Join(w.NginxAvailable, "whacp-webmail.conf"))
		pools, _ := filepath.Glob(filepath.Join(w.PoolDir("*"), "whacp-webmail.conf"))
		for _, p := range pools {
			os.Remove(p)
			v := filepath.Base(filepath.Dir(filepath.Dir(p)))
			exec.Command("systemctl", "reload", w.FPMUnit(v)).Run()
		}
		exec.Command("systemctl", "reload", "nginx").Run()
		os.RemoveAll(w.WebmailDir)
		os.RemoveAll(w.WebmailData)
		exec.Command("userdel", webmailUser).Run()
	})
	res := must("mail.webmail_configure", webmailParams(hostname))
	if res["url"] != "https://"+hostname+"/" {
		t.Fatalf("webmail = %v", res)
	}
	// Changing it again keeps the encryption key (and signs nobody out).
	cfg1, _ := os.ReadFile(env.M.WebmailDir + "/config.inc.php")
	must("mail.webmail_configure", webmailParams(hostname))
	if cfg2, _ := os.ReadFile(env.M.WebmailDir + "/config.inc.php"); string(cfg1) != string(cfg2) {
		t.Fatal("configuration (and key) changed on a repeated call")
	}
	if st, _ := os.Stat(env.M.WebmailData + "/roundcube.db"); st == nil {
		t.Fatal("no database")
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 30 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &cryptotls.Config{InsecureSkipVerify: true, ServerName: hostname},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:443")
		},
	}}
	base := "https://" + hostname + "/"
	get := func(path string) string {
		t.Helper()
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	post := func(path string, form url.Values) (int, string, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, base+path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Transport.RoundTrip(withCookies(req, jar))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if rc := resp.Cookies(); len(rc) > 0 {
			jar.SetCookies(req.URL, rc)
		}
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Location"), string(b)
	}
	tokenRe := regexp.MustCompile(`"request_token":"([^"]+)"`)
	login := func(pass string) (int, string) {
		page := get("")
		m := regexp.MustCompile(`name="_token" value="([^"]+)"`).FindStringSubmatch(page)
		if m == nil {
			t.Fatalf("no sign-in form:\n%.2000s", page)
		}
		code, loc, _ := post("?_task=login", url.Values{"_token": {m[1]}, "_task": {"login"}, "_action": {"login"}, "_timezone": {"UTC"}, "_url": {""}, "_user": {"bob@rt-mail.test"}, "_pass": {pass}})
		return code, loc
	}
	if code, loc := login("wrong-password"); code == http.StatusFound && strings.Contains(loc, "_task=mail") {
		t.Fatal("webmail accepted a wrong password")
	}
	if code, loc := login("Bob-mail-pass-12"); code != http.StatusFound || !strings.Contains(loc, "_task=mail") {
		log, _ := os.ReadFile(env.M.WebmailData + "/logs/errors.log")
		t.Fatalf("webmail sign-in: %d %s\n%s", code, loc, log)
	}
	compose := get("?_task=mail&_action=compose")
	id := regexp.MustCompile(`"compose_id":"([^"]+)"`).FindStringSubmatch(compose)
	tok := tokenRe.FindStringSubmatch(compose)
	from := regexp.MustCompile(`<select name="_from"[^>]*>\s*<option value="([0-9]+)"`).FindStringSubmatch(compose)
	if id == nil || tok == nil || from == nil {
		t.Fatalf("compose page:\n%.3000s", compose)
	}
	_, _, body := post("?_task=mail&_action=send", url.Values{"_token": {tok[1]}, "_task": {"mail"}, "_action": {"send"}, "_id": {id[1]}, "_from": {from[1]},
		"_to": {"alice@rt-mail.test"}, "_subject": {"from webmail"}, "_message": {"sent with roundcube"}, "_is_html": {"0"}, "_framed": {"1"}})
	if !strings.Contains(body, "sent_successfully") {
		log, _ := os.ReadFile(env.M.WebmailData + "/logs/errors.log")
		t.Fatalf("send:\n%.2000s\n%s", body, log)
	}
	found := false
	for i := 0; i < 3 && !found; i++ {
		for _, f := range waitMail(3) {
			if b, _ := os.ReadFile(f); strings.Contains(string(b), "sent with roundcube") {
				found = strings.Contains(string(b), "From: bob@rt-mail.test") && strings.Contains(string(b), "DKIM-Signature:")
				if !found {
					t.Fatalf("webmail message:\n%s", b)
				}
			}
		}
		time.Sleep(time.Second)
	}
	if !found {
		t.Fatal("webmail message not delivered")
	}
	// Package files are never changed.
	if b, _ := os.ReadFile(env.M.RoundcubeDefaults); strings.Contains(string(b), "WhaCP") {
		t.Fatal("package defaults changed")
	}

	must("mail.webmail_configure", map[string]any{"enabled": false})
	if _, err := os.Stat(filepath.Join(env.M.NginxEnabled, "whacp-webmail.conf")); err == nil {
		t.Fatal("webmail site left enabled")
	}
	if _, err := os.Stat(env.M.WebmailData + "/roundcube.db"); err != nil {
		t.Fatal("turning webmail off removed its data")
	}
}

// withCookies adds the jar's cookies to a request sent without the client
// (so redirects are not followed).
func withCookies(req *http.Request, jar http.CookieJar) *http.Request {
	for _, c := range jar.Cookies(req.URL) {
		req.AddCookie(c)
	}
	return req
}

// webmailParams turns webmail on; WHACP_TEST_WEBMAIL_PHP picks its PHP
// version where the distribution's does not run Roundcube (Ubuntu 26.04).
func webmailParams(hostname string) map[string]any {
	p := map[string]any{"enabled": true, "hostname": hostname}
	if v := os.Getenv("WHACP_TEST_WEBMAIL_PHP"); v != "" {
		p["php_version"] = v
	}
	return p
}
