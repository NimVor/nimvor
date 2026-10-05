package phpmgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func write(t *testing.T, h *testhost.Host, p, content string) {
	t.Helper()
	os.MkdirAll(filepath.Join(h.Base, filepath.Dir(p)), 0o755)
	if err := os.WriteFile(filepath.Join(h.Base, p), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func call(t *testing.T, r *actions.Registry, name string, params any) (any, error) {
	t.Helper()
	a, err := r.Lookup(name)
	if err != nil {
		t.Fatalf("no action %s", name)
	}
	b, _ := json.Marshal(params)
	return a.Handler(context.Background(), b)
}

func waitJob(t *testing.T, h *testhost.Host, res any) jobs.Job {
	t.Helper()
	id := res.(map[string]any)["job_id"].(string)
	for i := 0; i < 200; i++ {
		if j, err := h.Env.Jobs.Get(id); err == nil && (j.Status == jobs.Succeeded || j.Status == jobs.Failed) {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return jobs.Job{}
}

// Debian: states from dpkg and conf.d links; phpenmod/phpdismod; the
// extensions the panel needs are protected on the panel's PHP.
func TestExtensionsDebian(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	for _, m := range []string{"intl", "mysqlnd", "mysqli", "pdo_mysql", "mbstring"} {
		write(t, h, "etc/php/8.3/mods-available/"+m+".ini", "extension="+m+"\n")
	}
	for _, m := range []string{"intl", "mysqlnd", "mysqli", "mbstring"} { // pdo_mysql switched off
		write(t, h, "etc/php/8.3/fpm/conf.d/20-"+m+".ini", "extension="+m+"\n")
	}
	// Ubuntu: /usr/bin/php → /etc/alternatives/php → /usr/bin/php8.3.
	os.MkdirAll(filepath.Join(h.Base, "etc/alternatives"), 0o755)
	os.Remove(filepath.Join(h.Base, "etc/alternatives/php"))
	if err := os.Symlink("/usr/bin/php8.3", filepath.Join(h.Base, "etc/alternatives/php")); err != nil {
		t.Fatal(err)
	}
	h.Respond = func(id string, args []string) (string, error, bool) {
		switch id {
		case "dpkg-query":
			return "php8.3-intl\tii \t8.3.6\nphp8.3-mysql\tii \t8.3.6\nphp8.3-mbstring\tii \t8.3.6\n", nil, true
		case "apt-get":
			if len(args) > 1 && args[0] == "-s" {
				if args[len(args)-1] == "php8.3-igbinary" {
					return "Remv php8.3-igbinary [3.2]\nRemv php8.3-redis [6.0]\n", nil, true
				}
				return "Remv " + args[len(args)-1] + " [1]\n", nil, true
			}
		}
		return "", nil, false
	}

	res, err := call(t, r, "php.extensions", map[string]any{"version": "8.3"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ExtensionState{}
	for _, s := range res.([]ExtensionState) {
		got[s.Name] = s
	}
	if s := got["intl"]; !s.Installed || !s.Enabled || s.Version != "8.3.6" || s.Package != "php8.3-intl" {
		t.Errorf("intl = %+v", s)
	}
	if s := got["mysql"]; !s.Installed || s.Enabled {
		t.Errorf("mysql with pdo_mysql off = %+v", s)
	}
	if s := got["redis"]; s.Installed || s.Enabled {
		t.Errorf("redis = %+v", s)
	}
	if got["mbstring"].Protected == "" || got["intl"].Protected != "" {
		t.Errorf("protection: mbstring %q, intl %q", got["mbstring"].Protected, got["intl"].Protected)
	}

	if _, err := call(t, r, "php.extension_set", map[string]any{"version": "8.3", "name": "intl", "enabled": false}); err != nil {
		t.Fatal(err)
	}
	if !h.Runner.Called("phpdismod -v 8.3 intl") || !h.Runner.Called("php-fpm8.3 -t") {
		t.Errorf("calls: %v", h.Runner.Calls)
	}
	if _, err := call(t, r, "php.extension_set", map[string]any{"version": "8.3", "name": "mysql", "enabled": true}); err != nil || !h.Runner.Called("phpenmod -v 8.3 mysqlnd mysqli pdo_mysql") {
		t.Errorf("enable mysql: %v %v", err, h.Runner.Calls)
	}

	// PHP-FPM rejects the change: it is undone.
	h.Fail["php-fpm8.3 -t"] = "ERROR: failed"
	h.Runner.Calls = nil
	if _, err := call(t, r, "php.extension_set", map[string]any{"version": "8.3", "name": "intl", "enabled": true}); err == nil || !strings.Contains(err.Error(), "undone") {
		t.Fatalf("got %v", err)
	}
	if !h.Runner.Called("phpenmod -v 8.3 intl") || !h.Runner.Called("phpdismod -v 8.3 intl") {
		t.Errorf("not undone: %v", h.Runner.Calls)
	}
	delete(h.Fail, "php-fpm8.3 -t")

	// The panel's own PHP keeps what the panel needs.
	for _, a := range []string{"php.extension_set", "php.extension_remove"} {
		if _, err := call(t, r, a, map[string]any{"version": "8.3", "name": "mbstring", "enabled": false}); err == nil || !strings.Contains(err.Error(), "panel") {
			t.Errorf("%s mbstring: %v", a, err)
		}
	}
	// Unknown names and versions, and switching what is not installed.
	for _, bad := range []map[string]any{{"version": "8.3", "name": "evil; rm"}, {"version": "8.3;x", "name": "intl"}, {"version": "8.3", "name": "redis", "enabled": true}} {
		if _, err := call(t, r, "php.extension_set", bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}

	// Install and remove are package jobs; a removal that would take other
	// packages along removes nothing.
	res, err = call(t, r, "php.extension_install", map[string]any{"version": "8.3", "name": "redis"})
	if err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, h, res); j.Status != jobs.Succeeded || !h.Runner.Called("apt-get install -y -q --no-install-recommends -o Dpkg::Options::=--force-confold -o DPkg::Lock::Timeout=300 -- php8.3-redis") {
		t.Fatalf("install: %+v %v", j, h.Runner.Calls)
	}
	res, _ = call(t, r, "php.extension_remove", map[string]any{"version": "8.3", "name": "igbinary"})
	if j := waitJob(t, h, res); j.Status != jobs.Failed || !strings.Contains(j.Error, "php8.3-redis") || h.Runner.Called("apt-get remove -y -q -o DPkg::Lock::Timeout=300 -- php8.3-igbinary") {
		t.Fatalf("remove with dependents: %+v", j)
	}
	res, _ = call(t, r, "php.extension_remove", map[string]any{"version": "8.3", "name": "intl"})
	if j := waitJob(t, h, res); j.Status != jobs.Succeeded || !h.Runner.Called("apt-get remove -y -q -o DPkg::Lock::Timeout=300 -- php8.3-intl") {
		t.Fatalf("remove: %+v %v", j, h.Runner.Calls)
	}
}

// RHEL (Remi): php.d files; switching comments the extension line out and
// back, and a rejected change restores the file.
func TestExtensionsRHEL(t *testing.T) {
	h := testhost.New(t)
	write(t, h, "etc/os-release", "ID=\"almalinux\"\nVERSION_ID=\"9.6\"\n")
	h.Env.Platform = platform.Detect(h.Env.Root)
	h.Env.M = h.Env.Platform.Managed()
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	redis := "; Enable redis extension module\nextension=redis.so\n"
	write(t, h, "etc/opt/remi/php83/php.d/50-redis.ini", redis)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "rpm" {
			return "php83-php-pecl-redis6\t6.1.0-1.el9\n", nil, true
		}
		return "", nil, false
	}
	res, _ := call(t, r, "php.extensions", map[string]any{"version": "8.3"})
	for _, s := range res.([]ExtensionState) {
		if s.Name == "redis" && (!s.Installed || !s.Enabled || s.Package != "php83-php-pecl-redis6") {
			t.Errorf("redis = %+v", s)
		}
	}
	if _, err := call(t, r, "php.extension_set", map[string]any{"version": "8.3", "name": "redis", "enabled": false}); err != nil {
		t.Fatal(err)
	}
	if got := h.Read("etc/opt/remi/php83/php.d/50-redis.ini"); got != "; Enable redis extension module\n; whacp-disabled: extension=redis.so\n" {
		t.Fatalf("disabled file: %q", got)
	}
	if moduleEnabled(h.Env, "8.3", "redis") {
		t.Error("still reported enabled")
	}
	h.Fail["php-fpm8.3 -t"] = "ERROR"
	if _, err := call(t, r, "php.extension_set", map[string]any{"version": "8.3", "name": "redis", "enabled": true}); err == nil {
		t.Fatal("rejected change accepted")
	}
	delete(h.Fail, "php-fpm8.3 -t")
	if got := h.Read("etc/opt/remi/php83/php.d/50-redis.ini"); !strings.Contains(got, "; whacp-disabled: extension=redis.so") {
		t.Fatalf("rejected change not undone: %q", got)
	}
	if _, err := call(t, r, "php.extension_set", map[string]any{"version": "8.3", "name": "redis", "enabled": true}); err != nil {
		t.Fatal(err)
	}
	if got := h.Read("etc/opt/remi/php83/php.d/50-redis.ini"); got != redis {
		t.Fatalf("enabled file: %q", got)
	}
	// Part of the base packages on RHEL: switch, not install.
	if _, err := call(t, r, "php.extension_install", map[string]any{"version": "8.3", "name": "curl"}); err == nil || !strings.Contains(err.Error(), "base packages") {
		t.Errorf("curl install: %v", err)
	}
}
