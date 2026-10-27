package security

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestParseVerifyLine(t *testing.T) {
	cases := []struct {
		line        string
		wantPath    string
		wantMissing bool
		wantFlag    string // a character we expect in the flags
	}{
		{"??5??????   /etc/nginx/nginx.conf", "/etc/nginx/nginx.conf", false, "5"},        // dpkg checksum
		{"??5?????? c /etc/ssh/sshd_config", "/etc/ssh/sshd_config", false, "5"},          // dpkg, config
		{"missing     /etc/nginx/mime.types", "/etc/nginx/mime.types", true, ""},          // dpkg missing
		{"S.5....T.  c /etc/php/8.3/fpm/php.ini", "/etc/php/8.3/fpm/php.ini", false, "5"}, // rpm
		{".M.......    /usr/sbin/nginx", "/usr/sbin/nginx", false, "M"},                   // rpm mode
		{"missing   c /etc/my.cnf.d/server.cnf", "/etc/my.cnf.d/server.cnf", true, ""},
	}
	for _, c := range cases {
		flags, path, missing := parseVerifyLine(c.line)
		if path != c.wantPath || missing != c.wantMissing {
			t.Errorf("%q → path %q missing %v, want %q %v", c.line, path, missing, c.wantPath, c.wantMissing)
		}
		if c.wantFlag != "" && !containsRune(flags, c.wantFlag) {
			t.Errorf("%q → flags %q missing %q", c.line, flags, c.wantFlag)
		}
	}
}

func containsRune(s, set string) bool {
	for _, r := range set {
		for _, x := range s {
			if r == x {
				return true
			}
		}
	}
	return false
}

func TestUnderCriticalRoot(t *testing.T) {
	for _, p := range []string{"/etc/nginx/nginx.conf", "/usr/sbin/nginx", "/etc/ssh/sshd_config", "/etc/sudoers"} {
		if !underCriticalRoot(p) {
			t.Errorf("%s should be critical", p)
		}
	}
	for _, p := range []string{"/var/www/html/index.php", "/home/whacp/a.com/x", "/tmp/foo", "/etc/motd"} {
		if underCriticalRoot(p) {
			t.Errorf("%s should not be critical", p)
		}
	}
}

// The WhaCP baseline catches a changed, re-permissioned, deleted or new
// WhaCP-owned file. The package tool is the FakeRunner here, so only the
// baseline half runs.
func TestIntegrityBaselineDrift(t *testing.T) {
	h := testhost.New(t)
	env := h.Env
	// A WhaCP-owned configuration file.
	confReal, err := env.Root.Path("/etc/whacp")
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(confReal, 0o755)
	agentConf := filepath.Join(confReal, "agent.conf")
	os.WriteFile(agentConf, []byte("listen = /run/whacp.sock\n"), 0o640)

	if n, err := WriteIntegrityBaseline(env); err != nil || n == 0 {
		t.Fatalf("baseline: n=%d %v", n, err)
	}

	// No change yet: no WhaCP findings.
	r := CheckIntegrity(context.Background(), env, time.Now())
	for _, f := range r.Findings {
		if f.Class == "whacp" {
			t.Errorf("unexpected WhaCP finding before any change: %+v", f)
		}
	}
	if !r.HasBaseline {
		t.Fatal("report should see the baseline")
	}

	// Modify the file: a checksum change, graded high.
	os.WriteFile(agentConf, []byte("listen = /run/evil.sock\n"), 0o640)
	if f := whacpFinding(CheckIntegrity(context.Background(), env, time.Now()), agentConf); f == nil || f.Issue != "modified" || f.Severity != "high" {
		t.Fatalf("modified not detected: %+v", f)
	}

	// Change permissions only.
	os.WriteFile(agentConf, []byte("listen = /run/whacp.sock\n"), 0o640) // restore content
	WriteIntegrityBaseline(env)
	os.Chmod(agentConf, 0o666)
	if f := whacpFinding(CheckIntegrity(context.Background(), env, time.Now()), agentConf); f == nil || f.Issue != "permissions" {
		t.Fatalf("permission change not detected: %+v", f)
	}

	// A new WhaCP-owned file after the baseline.
	os.Chmod(agentConf, 0o640)
	WriteIntegrityBaseline(env)
	newFile := filepath.Join(confReal, "extra.conf")
	os.WriteFile(newFile, []byte("x\n"), 0o640)
	if f := whacpFinding(CheckIntegrity(context.Background(), env, time.Now()), newFile); f == nil || f.Issue != "new" {
		t.Fatalf("new file not detected: %+v", f)
	}

	// Delete a baselined file.
	WriteIntegrityBaseline(env)
	os.Remove(newFile)
	if f := whacpFinding(CheckIntegrity(context.Background(), env, time.Now()), newFile); f == nil || f.Issue != "deleted" {
		t.Fatalf("deletion not detected: %+v", f)
	}
}

func whacpFinding(r IntegrityReport, path string) *IntegrityFinding {
	for i := range r.Findings {
		if r.Findings[i].Path == path && r.Findings[i].Class == "whacp" {
			return &r.Findings[i]
		}
	}
	return nil
}
