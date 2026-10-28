package phpmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// The test server has PHP 8.3 (used by example.com) and 8.4 (unused);
// the repositories offer 8.2, 8.3 and 8.4.
func profileHost(t *testing.T, installed ...string) *testhost.Host {
	h := testhost.New(t)
	// PHP 8.1: installed, no website uses it.
	os.MkdirAll(filepath.Join(h.Base, "etc/php/8.1/fpm/pool.d"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "usr/sbin/php-fpm8.1"), []byte("x"), 0o755)
	if installed == nil {
		installed = []string{"php8.3-curl", "php8.3-xdebug", "php8.1-fpm", "php8.1-cli", "php8.1-common"}
	}
	have := map[string]bool{}
	for _, p := range installed {
		have[p] = true
	}
	for _, f := range []string{"etc/php/8.3/mods-available/curl.ini", "etc/php/8.3/mods-available/xdebug.ini"} {
		os.MkdirAll(filepath.Join(h.Base, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(h.Base, f), []byte("extension=x\n"), 0o644)
	}
	os.WriteFile(filepath.Join(h.Base, "etc/php/8.3/fpm/conf.d/20-curl.ini"), []byte("extension=curl\n"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "etc/php/8.3/fpm/conf.d/20-xdebug.ini"), []byte("zend_extension=xdebug\n"), 0o644)
	h.Respond = func(id string, a []string) (string, error, bool) {
		switch {
		case id == "apt-cache" && len(a) > 0 && a[0] == "search":
			return "php8.2-fpm - x\nphp8.3-fpm - x\nphp8.4-fpm - x\n", nil, true
		case id == "dpkg-query":
			var b strings.Builder
			for _, p := range a {
				if have[p] {
					fmt.Fprintf(&b, "%s\tii \t1.0\n", p)
				}
			}
			return b.String(), nil, true
		}
		return "", nil, false
	}
	return h
}

func TestProfilePlan(t *testing.T) {
	h := profileHost(t)
	plan, err := ProfilePlan(context.Background(), h.Env, Profile{Versions: []string{"8.3", "8.2", "8.0"}, Extensions: []string{"curl", "redis"}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Item{}
	for _, it := range plan.Items {
		got[it.Version+"/"+it.Extension] = it
	}
	expect := map[string]string{
		"8.2/":       ActInstall,   // offered, not installed
		"8.0/":       ActBlocked,   // not offered
		"8.3/":       ActUnchanged, // installed
		"8.3/curl":   ActUnchanged, // wanted and there
		"8.3/redis":  ActInstall,   // wanted, missing
		"8.3/xdebug": ActRemove,    // installed, not wanted
		"8.1/":       ActRemove,    // installed, not in the profile, unused
		"8.4/":       ActBlocked,   // not in the profile, but shop.example.com uses it
	}
	for k, want := range expect {
		if got[k].Action != want {
			t.Errorf("%s: %q (%s), want %q", k, got[k].Action, got[k].Detail, want)
		}
	}
	if p := strings.Join(got["8.2/"].Packages, " "); p != "php8.2-fpm php8.2-cli php8.2-curl php8.2-redis" {
		t.Errorf("8.2 packages: %s", p)
	}
	if p := strings.Join(got["8.1/"].Packages, " "); p != "php8.1-cli php8.1-common php8.1-fpm" {
		t.Errorf("8.1 removal packages: %s", p)
	}
	if !strings.Contains(got["8.4/"].Detail, "shop.example.com") {
		t.Errorf("8.4: %+v", got["8.4/"])
	}
	if !plan.Changes || !plan.Removals {
		t.Errorf("plan flags: %+v", plan)
	}
	// The panel's own PHP is never removed.
	plan, _ = ProfilePlan(context.Background(), h.Env, Profile{Versions: []string{"8.4"}, Extensions: []string{}})
	for _, it := range plan.Items {
		if it.Version == "8.3" && it.Extension == "" && (it.Action != ActBlocked || !strings.Contains(it.Detail, "panel")) {
			t.Errorf("8.3 (the panel's): %+v", it)
		}
	}
	if _, err := ProfilePlan(context.Background(), h.Env, Profile{Versions: []string{"8.3; rm"}}); err == nil {
		t.Error("bad version accepted")
	}
	if _, err := ProfilePlan(context.Background(), h.Env, Profile{Versions: []string{"8.3"}, Extensions: []string{"evil"}}); err == nil {
		t.Error("unknown extension accepted")
	}
}

func TestProfileApply(t *testing.T) {
	h := profileHost(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("php.profile_apply")
	body := json.RawMessage(`{"versions":["8.3","8.2"],"extensions":["curl","redis"]}`)
	if _, err := a.Handler(context.Background(), body); err == nil || !strings.Contains(err.Error(), "confirm the removals") {
		t.Fatalf("removals without confirmation: %v", err)
	}
	res, err := a.Handler(context.Background(), json.RawMessage(`{"versions":["8.3","8.2"],"extensions":["curl","redis"],"confirm_removals":true}`))
	if err != nil {
		t.Fatal(err)
	}
	j, _ := h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string))
	if j.Status != "succeeded" {
		t.Fatalf("job %+v", j)
	}
	for _, want := range []string{
		"apt-get install -y -q --no-install-recommends -o Dpkg::Options::=--force-confold -o DPkg::Lock::Timeout=300 -- php8.2-fpm php8.2-cli php8.2-curl php8.2-redis",
		"apt-get install -y -q --no-install-recommends -o Dpkg::Options::=--force-confold -o DPkg::Lock::Timeout=300 -- php8.3-redis",
		"apt-get remove -y -q -o DPkg::Lock::Timeout=300 -- php8.3-xdebug",
		"apt-get remove -y -q -o DPkg::Lock::Timeout=300 -- php8.1-cli php8.1-common php8.1-fpm",
		"systemctl disable -- php8.1-fpm.service",
		"systemctl enable -- php8.2-fpm.service",
	} {
		if !h.Runner.Called(want) {
			t.Errorf("missing %q\n%s", want, strings.Join(h.Runner.Calls, "\n"))
		}
	}
	// Already matching: nothing to apply.
	h2 := profileHost(t, "php8.1-fpm")
	r2 := actions.NewRegistry(true)
	Register(r2, h2.Env)
	a2, _ := r2.Lookup("php.profile_apply")
	_, err = a2.Handler(context.Background(), json.RawMessage(`{"versions":["8.1","8.3","8.4"],"extensions":[]}`))
	if err == nil || !strings.Contains(err.Error(), "Nothing to change") {
		t.Fatalf("matching profile: %v", err)
	}
}
