//go:build realhost

package mailmgr

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/sendmail"
)

// This test binary stands in for the agent binary: started as sendmail
// (bind-mounted in a unit), it runs WhaCP's sendmail.
func TestMain(m *testing.M) {
	if strings.HasPrefix(filepath.Base(os.Args[0]), "sendmail") {
		os.Exit(sendmail.Main(os.Args[1:], os.Stdin, os.Stderr))
	}
	os.Exit(m.Run())
}

// A command run as a website account (deploy hook, cron "run now") sends
// mail the way PHP's mail() does, "sendmail -t -i", and it is delivered.
// Postfix's own sendmail would hang there (setgid postdrop under
// NoNewPrivileges, F-25).
func testSendmailShim(t *testing.T, env *host.Env, waitMail func(int) []string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	old := execx.SendmailShim
	execx.SendmailShim = exe
	t.Cleanup(func() { execx.SendmailShim = old })

	name := "whacpsm" + strconv.Itoa(os.Getpid()%100000)
	home := "/home/" + name
	if out, err := exec.Command("useradd", "-m", "-d", home, "-s", "/usr/sbin/nologin", name).CombinedOutput(); err != nil {
		t.Fatalf("useradd: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("userdel", "-r", name).Run() })
	u, _ := user.Lookup(name)
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	before := len(waitMail(1))
	script := `printf 'To: alice@rt-mail.test\nBcc: bob@rt-mail.test\nSubject: from a deploy hook\n\nsent with sendmail -t -i\n' | sendmail -t -i`
	out, err := env.Runner.RunWith(context.Background(), "sh", execx.Opts{As: &execx.Credential{UID: uint32(uid), GID: uint32(gid), Home: home}, Dir: home}, "-c", script)
	if err != nil {
		t.Fatalf("sendmail as %s: %v %s", name, err, out)
	}
	files := waitMail(before + 1)
	found := false
	for _, f := range files {
		b, _ := os.ReadFile(f)
		if strings.Contains(string(b), "Subject: from a deploy hook") {
			found = true
			if !strings.Contains(string(b), "From: "+name+"@") || strings.Contains(string(b), "Bcc:") {
				t.Errorf("delivered message:\n%s", b)
			}
		}
	}
	if !found {
		t.Fatal("the message sent with sendmail was not delivered")
	}
	// The distribution's sendmail itself is unchanged outside the unit.
	for _, p := range []string{"/usr/sbin/sendmail"} {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			if b, _ := os.ReadFile(real); strings.Contains(string(b), "sendmail (WhaCP)") {
				t.Errorf("%s was replaced on the host", p)
			}
		}
	}
}
