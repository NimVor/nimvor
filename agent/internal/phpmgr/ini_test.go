package phpmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/platform"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestParseIni(t *testing.T) {
	tz := func(z string) bool { return z == "UTC" || z == "Asia/Karachi" }
	got, err := ParseIni("; comment\nmemory_limit = 512m\ndisplay_errors=yes\ndate.timezone = \"Asia/Karachi\"\nerror_reporting = E_ALL & ~E_DEPRECATED\nopcache.jit=Tracing\n", tz)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"memory_limit": "512M", "display_errors": "On", "date.timezone": `"Asia/Karachi"`, "error_reporting": "E_ALL & ~E_DEPRECATED", "opcache.jit": "tracing"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	for _, bad := range []string{
		"extension = /tmp/evil.so", "zend_extension=x.so", "auto_prepend_file = /tmp/x.php", "open_basedir = /",
		"session.save_path = /tmp", "sendmail_path = /bin/sh -c id", "disable_functions =", "[PHP]",
		"memory_limit = 12Q", "memory_limit = 1M; extension=x", "max_input_vars = 5", "date.timezone = Foo/Bar",
		"error_reporting = E_ALL; system", "memory_limit = 1M\nmemory_limit = 2M", "display_errors = maybe",
	} {
		if _, err := ParseIni(bad, tz); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// Debian writes PHP-FPM's and the command line's file; RHEL php.d. PHP's
// warnings count as errors and roll back; empty settings remove the files.
func TestIniSet(t *testing.T) {
	for _, c := range []struct {
		name, osRelease string
		files           []string
	}{
		{"ubuntu", "", []string{"etc/php/8.3/fpm/conf.d/99-whacp.ini", "etc/php/8.3/cli/conf.d/99-whacp.ini"}},
		{"rhel", "ID=\"almalinux\"\nVERSION_ID=\"9.6\"\n", []string{"etc/opt/remi/php83/php.d/99-whacp.ini"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := testhost.New(t)
			if c.osRelease != "" {
				write(t, h, "etc/os-release", c.osRelease)
				h.Env.Platform = platform.Detect(h.Env.Root)
				h.Env.M = h.Env.Platform.Managed()
			}
			for _, f := range c.files {
				os.MkdirAll(filepath.Join(h.Base, filepath.Dir(f)), 0o755)
			}
			r := actions.NewRegistry(true)
			Register(r, h.Env)
			if _, err := call(t, r, "php.ini_set", map[string]any{"version": "8.3", "content": "max_input_vars = 5000\nupload_max_filesize = 128M\n"}); err != nil {
				t.Fatal(err)
			}
			for _, f := range c.files {
				if got := h.Read(f); !strings.HasPrefix(got, "; Managed by WhaCP") || !strings.HasSuffix(got, "\nupload_max_filesize = 128M\nmax_input_vars = 5000\n") {
					t.Errorf("%s:\n%s", f, got)
				}
			}
			if !h.Runner.Called("php-fpm8.3 -t") {
				t.Errorf("not checked: %v", h.Runner.Calls)
			}

			// PHP warns about a value: the previous file stays.
			h.Respond = func(id string, args []string) (string, error, bool) {
				if id == "php-fpm8.3" && len(args) == 1 && args[0] == "-t" {
					return "NOTICE: PHP message: PHP Warning:  Failed to set memory limit to 1024 bytes in Unknown on line 0\nNOTICE: configuration file test is successful\n", nil, true
				}
				return "", nil, false
			}
			if _, err := call(t, r, "php.ini_set", map[string]any{"version": "8.3", "content": "memory_limit = 1K\n"}); err == nil || !strings.Contains(err.Error(), "not applied") {
				t.Fatalf("warning accepted: %v", err)
			}
			if !strings.Contains(h.Read(c.files[0]), "max_input_vars = 5000") {
				t.Error("previous settings not restored")
			}
			h.Respond = nil

			if _, err := call(t, r, "php.ini_set", map[string]any{"version": "8.3", "content": "extension=/tmp/evil.so"}); err == nil {
				t.Error("extension= accepted")
			}
			if _, err := call(t, r, "php.ini_set", map[string]any{"version": "8.3", "content": ""}); err != nil {
				t.Fatal(err)
			}
			for _, f := range c.files {
				if h.Exists(f) {
					t.Errorf("%s left after clearing", f)
				}
			}
		})
	}
}
