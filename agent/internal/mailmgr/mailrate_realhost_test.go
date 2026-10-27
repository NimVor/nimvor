//go:build realhost

package mailmgr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/limits"
	"github.com/NimVor/nimvor/agent/internal/mailrate"
)

// Outgoing mail limits against a real Postfix: a website account sends
// through WhaCP's sendmail; the policy service finds the account from the
// socket table, lets the first two recipients through and defers the
// third; Postfix's own sendmail refuses the account; root is not limited.
func TestRealHostMailRate(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("postconf"); err != nil {
		t.Skip("needs Postfix")
	}
	dir := t.TempDir()
	os.Chmod(dir, 0o755)
	bin := filepath.Join(dir, "whacp-agent")
	if _, err := exec.LookPath("go"); err != nil {
		if out, err := exec.Command("cp", "../../../dist/whacp-agent", bin).CombinedOutput(); err != nil {
			t.Fatalf("copy: %s", out)
		}
	} else if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/whacp-agent").CombinedOutput(); err != nil {
		t.Fatalf("build: %s", out)
	}
	// Executable and readable by the account (the test folder is not).
	sysBin := "/usr/local/bin/whacp-agent-mailrate-test"
	if out, err := exec.Command("install", "-m", "0755", bin, sysBin).CombinedOutput(); err != nil {
		t.Fatalf("install: %s", out)
	}
	bin = sysBin
	pub := "/etc/whacp-mailrate-test"
	os.MkdirAll(pub, 0o755)
	t.Cleanup(func() { os.Remove(sysBin); os.RemoveAll(pub) })

	// The units as the agent writes them.
	if _, err := exec.LookPath("systemd-analyze"); err == nil {
		unitDir := filepath.Join(dir, "units")
		os.Mkdir(unitDir, 0o755)
		os.WriteFile(filepath.Join(unitDir, MailRateSocket), []byte(renderMailRateSocket("/var/spool/postfix")), 0o644)
		os.WriteFile(filepath.Join(unitDir, MailRateService), []byte(renderMailRateService(bin, filepath.Join(dir, "mailrate.json"))), 0o644)
		out, _ := exec.Command("systemd-analyze", "verify", "--man=no", filepath.Join(unitDir, MailRateService), filepath.Join(unitDir, MailRateSocket)).CombinedOutput()
		for _, l := range verifyErrors(out) {
			t.Errorf("systemd-analyze verify: %s", l)
		}
		if help, _ := exec.Command("systemd-analyze", "--help").CombinedOutput(); strings.Contains(string(help), "--offline") {
			sec, _ := exec.Command("systemd-analyze", "security", "--offline=true", "--no-pager", filepath.Join(unitDir, MailRateService)).CombinedOutput()
			last := strings.TrimSpace(string(sec))
			last = last[strings.LastIndex(last, "\n")+1:]
			t.Logf("systemd-analyze security: %s", last)
			if !strings.Contains(last, "OK") {
				t.Errorf("exposure not OK: %s", last)
			}
		}
	}

	// A website account.
	name := fmt.Sprintf("wcpmr%d", os.Getpid()%100000)
	if out, err := exec.Command("useradd", "-M", "-s", "/usr/sbin/nologin", "-K", "UID_MIN=2000", name).CombinedOutput(); err != nil {
		t.Skipf("useradd: %s", out)
	}
	t.Cleanup(func() { exec.Command("userdel", name).Run() })
	u, err := user.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)

	cfg := mailrate.Config{Enabled: true, PerHour: 2, Names: map[string]string{u.Uid: name}}
	b, _ := json.Marshal(cfg)
	os.WriteFile(filepath.Join(dir, "mailrate.json"), b, 0o600)
	os.WriteFile(filepath.Join(pub, limits.NoSubmitFile), []byte(limits.RenderNoSubmit([]string{name})), 0o644)
	state, run := filepath.Join(dir, "state"), filepath.Join(dir, "run")
	os.Mkdir(state, 0o700)
	os.Mkdir(run, 0o700)
	sock := "/var/spool/postfix/private/whacp-mailrate"
	args := []string{bin, "mailrate-policy", "-config", filepath.Join(dir, "mailrate.json"), "-state", state, "-run", run, "-listen", sock}
	if selinuxEnabled() {
		// As in production: a service started by systemd (its SELinux
		// domain, which the installer's module lets smtpd reach), not a
		// child of this test (unconfined_t).
		selinuxSockets(t)
		args = append([]string{"systemd-run", "--quiet", "--collect", "--wait", "--unit=whacp-mailrate-test", "--property=Type=exec"}, args...)
		t.Cleanup(func() { exec.Command("systemctl", "stop", "whacp-mailrate-test").Run() })
	}
	svc := exec.Command(args[0], args[1:]...)
	svc.Stderr = os.Stderr
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	exec.Command("chown", "postfix:postfix", sock).Run()
	if err := mailRateSelfTest(t.Context(), sock); err != nil {
		t.Fatalf("self-test: %v", err)
	}

	get := func(k string) string {
		out, _ := exec.Command("postconf", "-h", k).Output()
		return strings.TrimSpace(string(out))
	}
	prev := map[string]string{}
	for _, k := range []string{"smtpd_recipient_restrictions", "authorized_submit_users", "maillog_file"} {
		prev[k] = get(k)
	}
	set := func(k, v string) {
		if out, err := exec.Command("postconf", "-e", k+"="+v).CombinedOutput(); err != nil {
			t.Fatalf("postconf %s: %s", k, out)
		}
	}
	postfixStop()
	set("smtpd_recipient_restrictions", withMailRate(prev["smtpd_recipient_restrictions"], true))
	set("authorized_submit_users", submitLimited(pub))
	if _, err := os.Stat("/run/systemd/journal/socket"); err != nil {
		set("maillog_file", "/var/log/postfix.log")
	}
	sender := name + "@whacp-mailrate.invalid"
	t.Cleanup(func() {
		// Our test messages only.
		if out, err := exec.Command("postqueue", "-j").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				var q struct {
					ID     string `json:"queue_id"`
					Sender string `json:"sender"`
				}
				if json.Unmarshal([]byte(line), &q) == nil && strings.HasSuffix(q.Sender, "@whacp-mailrate.invalid") {
					exec.Command("postsuper", "-d", q.ID).Run()
				}
			}
		}
		postfixStop()
		for k, v := range prev {
			exec.Command("postconf", "-e", k+"="+v).Run()
		}
		// Running again, under systemd, as before the test.
		postfixStart()
		svc.Process.Signal(os.Interrupt)
		svc.Wait()
		os.Remove(sock)
	})
	if out, err := exec.Command("postfix", "check").CombinedOutput(); err != nil {
		t.Fatalf("postfix check: %s", out)
	}
	if out, err := postfixStart(); err != nil {
		t.Fatalf("postfix start: %s", out)
	}
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", "127.0.0.1:25"); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	send := func(prog string, args []string, asAccount bool) (int, string) {
		cmd := exec.Command(prog, args...)
		cmd.Stdin = strings.NewReader("To: someone@example.invalid\nSubject: limit test\n\nhello\n")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if asAccount {
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
		}
		err := cmd.Run()
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), stderr.String()
		}
		if err != nil {
			return -1, err.Error()
		}
		return 0, stderr.String()
	}
	bridge := []string{"sendmail", "-t", "-i", "-f", sender}
	for i := 1; i <= 2; i++ {
		if code, out := send(bin, bridge, true); code != 0 {
			t.Fatalf("message %d refused: %d %s", i, code, out)
		}
	}
	if code, out := send(bin, bridge, true); code != 75 || !strings.Contains(out, "4.7.1") {
		t.Fatalf("third message: want deferral (75, 4.7.1), got %d %s\n%s", code, out, mailDiagnosis("whacp-mailrate"))
	}
	// Root (a system account) is not limited.
	if code, out := send(bin, bridge, false); code != 0 {
		t.Fatalf("root limited: %d %s", code, out)
	}
	// Postfix's own sendmail refuses the account.
	if code, out := send("/usr/sbin/sendmail", []string{"-t", "-i", "-f", sender}, true); code == 0 || !strings.Contains(out, "not allowed") {
		t.Fatalf("Postfix sendmail as the account: %d %s", code, out)
	}
	// The event and the count are reported on the control socket.
	st, err := mailrate.Control(t.Context(), filepath.Join(run, "control"), mailrate.ControlRequest{Op: "status"})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Events) != 1 || st.Events[0].Name != name || len(st.Accounts) != 1 || !st.Accounts[0].Blocked {
		t.Fatalf("status: %+v", st)
	}
	// Reset: the account sends again.
	if _, err := mailrate.Control(t.Context(), filepath.Join(run, "control"), mailrate.ControlRequest{Op: "reset", UID: uid}); err != nil {
		t.Fatal(err)
	}
	if code, out := send(bin, bridge, true); code != 0 {
		t.Fatalf("after reset: %d %s", code, out)
	}
}
