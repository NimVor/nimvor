//go:build realhost

package mailmgr

import (
	"context"
	"encoding/json"
	"net"
	"net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/greylist"
)

// Greylisting against a real Postfix: the agent binary runs as the policy
// service; a client outside mynetworks is deferred with 4.7.1, the triplet
// shows in the report, and a trusted host is not deferred. The generated
// systemd units are checked by systemd-analyze.
func TestRealHostGreylisting(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("postconf"); err != nil {
		t.Skip("needs Postfix")
	}
	dir := t.TempDir()
	// The CI virtual machines have the built agent but no Go.
	bin := filepath.Join(dir, "whacp-agent")
	if _, err := exec.LookPath("go"); err != nil {
		if out, err := exec.Command("cp", "../../../dist/whacp-agent", bin).CombinedOutput(); err != nil {
			t.Fatalf("copy: %s", out)
		}
	} else if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/whacp-agent").CombinedOutput(); err != nil {
		t.Fatalf("build: %s", out)
	}
	// With systemd the real units run it (dynamic user, credential, socket
	// activation, SELinux contexts); without (containers) it runs directly.
	_, sdErr := os.Stat("/run/systemd/system")
	withSystemd := sdErr == nil

	// The units as the agent writes them.
	unitDir := filepath.Join(dir, "units")
	os.Mkdir(unitDir, 0o755)
	os.WriteFile(filepath.Join(unitDir, GreylistSocket), []byte(renderGreylistSocket("/var/spool/postfix")), 0o644)
	os.WriteFile(filepath.Join(unitDir, GreylistService), []byte(renderGreylistService(bin, filepath.Join(dir, "greylist.json"))), 0o644)
	if _, err := exec.LookPath("systemd-analyze"); err == nil {
		out, _ := exec.Command("systemd-analyze", "verify", "--man=no", filepath.Join(unitDir, GreylistService), filepath.Join(unitDir, GreylistSocket)).CombinedOutput()
		for _, l := range verifyErrors(out) {
			t.Errorf("systemd-analyze verify: %s", l)
		}
		// --offline needs systemd 250 or later (Ubuntu 22.04 has 249).
		if help, _ := exec.Command("systemd-analyze", "--help").CombinedOutput(); strings.Contains(string(help), "--offline") {
			sec, _ := exec.Command("systemd-analyze", "security", "--offline=true", "--no-pager", filepath.Join(unitDir, GreylistService)).CombinedOutput()
			last := strings.TrimSpace(string(sec))
			last = last[strings.LastIndex(last, "\n")+1:]
			t.Logf("systemd-analyze security: %s", last)
			if !strings.Contains(last, "OK") {
				t.Errorf("exposure not OK: %s", last)
			}
		}
	}

	var cfg = greylist.Config{Enabled: true, Deferral: 5, Resend: 60, Expire: 1440}
	cfg.Normalize()
	for _, p := range cfg.Providers {
		p.Trust = false
	}
	var writeCfg func()
	writeCfg = func() {
		b, _ := json.Marshal(cfg)
		os.WriteFile(filepath.Join(dir, "greylist.json"), b, 0o600)
	}
	writeCfg()
	sock := "/var/spool/postfix/private/whacp-greylist"
	state, run := filepath.Join(dir, "state"), filepath.Join(dir, "run")
	control := filepath.Join(run, "control")
	os.Mkdir(state, 0o700)
	os.Mkdir(run, 0o700)
	var svc *exec.Cmd
	var start, stop func()
	if withSystemd {
		// Outside /tmp (the unit has PrivateTmp) and executable by the dynamic user.
		sysBin := "/usr/local/bin/whacp-agent-greylist-test"
		conf := "/etc/whacp-greylist-test.json"
		exec.Command("install", "-m", "0755", bin, sysBin).Run()
		os.WriteFile("/etc/systemd/system/"+GreylistSocket, []byte(renderGreylistSocket("/var/spool/postfix")), 0o644)
		os.WriteFile("/etc/systemd/system/"+GreylistService, []byte(renderGreylistService(sysBin, conf)), 0o644)
		exec.Command("systemctl", "daemon-reload").Run()
		control = greylistControl
		t.Cleanup(func() {
			exec.Command("systemctl", "stop", GreylistService, GreylistSocket).Run()
			os.Remove("/etc/systemd/system/" + GreylistSocket)
			os.Remove("/etc/systemd/system/" + GreylistService)
			os.Remove(sysBin)
			os.Remove(conf)
			exec.Command("systemctl", "daemon-reload").Run()
		})
		writeCfg = func() {
			b, _ := json.Marshal(cfg)
			os.WriteFile(conf, b, 0o600)
		}
		writeCfg()
		selinuxSockets(t)
		start = func() {
			if out, err := exec.Command("systemctl", "start", GreylistSocket).CombinedOutput(); err != nil {
				t.Fatalf("socket: %s", out)
			}
			// The first request starts the service (socket activation).
			if err := greylistSelfTest(context.Background(), sock); err != nil {
				out, _ := exec.Command("journalctl", "-u", GreylistService, "-n", "20", "--no-pager").CombinedOutput()
				t.Fatalf("self-test: %v\n%s", err, out)
			}
			if out, _ := exec.Command("systemctl", "show", "-p", "User,DynamicUser", "--value", GreylistService).Output(); !strings.Contains(string(out), "yes") {
				t.Errorf("not a dynamic user: %s", out)
			}
		}
		stop = func() { exec.Command("systemctl", "stop", GreylistService).Run() }
	} else {
		start = func() {
			svc = exec.Command(bin, "greylist-policy", "-config", filepath.Join(dir, "greylist.json"), "-state", state, "-run", run, "-listen", sock)
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
			if err := greylistSelfTest(context.Background(), sock); err != nil {
				t.Fatalf("self-test: %v", err)
			}
		}
		stop = func() {
			svc.Process.Signal(os.Interrupt)
			svc.Wait()
		}
	}
	start()

	get := func(k string) string {
		out, _ := exec.Command("postconf", "-h", k).Output()
		return strings.TrimSpace(string(out))
	}
	prev := map[string]string{}
	for _, k := range []string{"inet_interfaces", "mynetworks", "smtpd_recipient_restrictions", "maillog_file"} {
		prev[k] = get(k)
	}
	set := func(k, v string) {
		if out, err := exec.Command("postconf", "-e", k+"="+v).CombinedOutput(); err != nil {
			t.Fatalf("postconf %s: %s", k, out)
		}
	}
	postfixStop()
	set("inet_interfaces", "all")
	// Only loopback is trusted: RHEL's default (mynetworks_style=host)
	// trusts every address of this host, which is how the test connects.
	set("mynetworks", "127.0.0.0/8 [::1]/128")
	set("smtpd_recipient_restrictions", withGreylisting(prev["smtpd_recipient_restrictions"], true))
	if _, err := os.Stat("/run/systemd/journal/socket"); err != nil {
		set("maillog_file", "/var/log/postfix.log")
	}
	t.Cleanup(func() {
		postfixStop()
		for k, v := range prev {
			exec.Command("postconf", "-e", k+"="+v).Run()
		}
		// Running again, under systemd, as before the test.
		postfixStart()
		stop()
		if !withSystemd {
			os.Remove(sock)
		}
	})
	if out, err := exec.Command("postfix", "check").CombinedOutput(); err != nil {
		t.Fatalf("postfix check: %s", out)
	}
	if out, err := postfixStart(); err != nil {
		t.Fatalf("postfix start: %s", out)
	}

	// An address of this host that is not in mynetworks.
	var remote string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
			remote = n.IP.String()
		}
	}
	if remote == "" {
		t.Skip("needs a non-loopback IPv4 address")
	}
	// The client address Postfix sees (the kernel picks the source address).
	var src string
	rcpt := func() error {
		var conn net.Conn
		var err error
		for i := 0; i < 50; i++ {
			if conn, err = net.Dial("tcp", remote+":25"); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("smtp: %v", err)
		}
		src = conn.LocalAddr().(*net.TCPAddr).IP.String()
		c, err := smtp.NewClient(conn, remote)
		if err != nil {
			t.Fatalf("smtp: %v", err)
		}
		defer c.Close()
		if err := c.Hello("client.example.net"); err != nil {
			t.Fatal(err)
		}
		if err := c.Mail("sender@example.net"); err != nil {
			t.Fatal(err)
		}
		err = c.Rcpt("postmaster@localhost")
		c.Reset()
		return err
	}
	err := rcpt()
	if err == nil || !strings.Contains(err.Error(), "450") || !strings.Contains(err.Error(), "Greylisted") {
		t.Fatalf("first attempt not greylisted: %v\n%s", err, mailDiagnosis("whacp-greylist"))
	}
	var rep struct {
		Total int                  `json:"total"`
		Rows  []greylist.ReportRow `json:"rows"`
	}
	if err := greylist.Control(context.Background(), control, greylist.ControlRequest{Op: "report"}, &rep); err != nil {
		t.Fatal(err)
	}
	// The address Postfix saw (normally src; a host with NAT rules may
	// differ): not loopback.
	if rep.Total == 1 && rep.Rows[0].IP != src {
		t.Logf("client address %s, Postfix saw %s", src, rep.Rows[0].IP)
		src = rep.Rows[0].IP
	}
	if rep.Total != 1 || net.ParseIP(rep.Rows[0].IP).IsLoopback() || rep.Rows[0].From != "sender@example.net" || rep.Rows[0].To != "postmaster@localhost" || rep.Rows[0].Status != "deferring" {
		t.Fatalf("report: %+v", rep)
	}
	// State survives a restart; a trusted host is not deferred.
	stop()
	cfg.Trusted = []greylist.TrustedHost{{Range: src, Comment: "test"}}
	writeCfg()
	start()
	if err := rcpt(); err != nil {
		t.Fatalf("trusted host deferred: %v", err)
	}
	if err := greylist.Control(context.Background(), control, greylist.ControlRequest{Op: "report"}, &rep); err != nil || rep.Total != 1 || rep.Rows[0].Deferred != 1 {
		t.Fatalf("after restart: %v %+v", err, rep)
	}
}
