package pkgs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/platform"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// Installed components are found through dpkg-query (Ubuntu) and rpm
// (RHEL). Reported on a real server: every component showed "Install"
// because the query format held real line breaks, which the agent refuses
// in arguments, so the query never ran. The test runner applies the same
// argument check.
func TestInstalled(t *testing.T) {
	for _, c := range []struct {
		name, osRelease, tool, out string
		want                       map[string]string
	}{
		{"ubuntu", "ID=ubuntu\nVERSION_ID=\"24.04\"\n", "dpkg-query",
			"dpkg-query: no packages found matching rspamd\nnginx\tii \t1.24.0-2ubuntu7\nfail2ban\trc \t1.0.2-3\nmariadb-server\tii \t1:10.11.14\n",
			map[string]string{"nginx": "1.24.0-2ubuntu7", "mariadb-server": "1:10.11.14"}},
		{"rhel", "ID=\"almalinux\"\nVERSION_ID=\"9.6\"\n", "rpm",
			"package rspamd is not installed\nnginx\t1.20.1-22.el9\nmariadb-server\t10.5.27-1.el9\n",
			map[string]string{"nginx": "1.20.1-22.el9", "mariadb-server": "10.5.27-1.el9"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := testhost.New(t)
			os.WriteFile(filepath.Join(h.Base, "etc/os-release"), []byte(c.osRelease), 0o644)
			h.Env.Platform = platform.Detect(h.Env.Root)
			var args []string
			h.Respond = func(id string, a []string) (string, error, bool) {
				if id == c.tool {
					args = a
					return c.out, nil, true
				}
				return "", nil, false
			}
			got := Installed(context.Background(), h.Env, []string{"nginx", "fail2ban", "mariadb-server", "rspamd"})
			if len(got) != len(c.want) {
				t.Fatalf("got %v (query %q, calls %v)", got, args, h.Runner.Calls)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// Removing an extension must not take other packages along; Debian's
// unversioned metapackage of the extension itself may go.
func TestExtraRemovals(t *testing.T) {
	sim := "The following packages will be REMOVED:\n  php-mbstring php8.3-mbstring\nRemv php-mbstring [2:8.3+93ubuntu2]\nRemv php8.3-mbstring [8.3.6-0ubuntu0.24.04.11]\n"
	if got := ExtraRemovals(sim, []string{"php8.3-mbstring"}); len(got) != 0 {
		t.Errorf("metapackage counted as extra: %v", got)
	}
	sim += "Remv phpmyadmin [4:5.2.1]\nRemv libapache2-mod-php8.3:amd64 [8.3.6]\n"
	if got := ExtraRemovals(sim, []string{"php8.3-mbstring"}); len(got) != 2 || got[0] != "phpmyadmin" || got[1] != "libapache2-mod-php8.3" {
		t.Errorf("extras: %v", got)
	}
}
