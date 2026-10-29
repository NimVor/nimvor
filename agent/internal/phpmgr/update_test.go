package phpmgr

import (
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

const pendingUpgrades = `Inst php8.3-fpm [8.3.6-0ubuntu0.24.04.1] (8.3.6-0ubuntu0.24.04.2 Ubuntu:24.04/noble-security [amd64])
Inst php8.3-cli [8.3.6-0ubuntu0.24.04.1] (8.3.6-0ubuntu0.24.04.2 Ubuntu:24.04/noble-security [amd64])
Inst php8.3-redis [6.0-1] (6.1-1 Ubuntu:24.04/noble-updates [amd64])
Inst php8.4-cli [8.4.1-1] (8.4.2-1 Ubuntu:24.04/noble-updates [amd64])
Inst php8.30-fake [1] (2 Ubuntu:24.04/noble-updates [amd64])
Inst libssl3t64 [3.0.13] (3.0.14 Ubuntu:24.04/noble-security [amd64])
`

func TestPHPUpdatesAreListedPerVersion(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "apt-get" && len(args) > 0 && args[0] == "-s" {
			return pendingUpgrades, nil, true
		}
		return "", nil, false
	}
	res, err := call(t, r, "php.updates", nil)
	if err != nil {
		t.Fatal(err)
	}
	vs := res.(map[string]any)["versions"].(map[string]any)
	v83 := vs["8.3"].(map[string]any)
	if v83["count"] != 3 || v83["security"] != 2 {
		t.Errorf("8.3 = %v", v83)
	}
}

func TestOwnsPackage(t *testing.T) {
	h := testhost.New(t)
	for _, c := range []struct {
		v, name string
		want    bool
	}{{"8.3", "php8.3", true}, {"8.3", "php8.3-fpm", true}, {"8.3", "php8.30-fpm", false}, {"8.3", "php8.4-fpm", false}, {"8.3", "libssl3", false}, {"8.3", "php-common", false}} {
		if got := ownsPackage(h.Env, c.v, c.name); got != c.want {
			t.Errorf("%s %s = %v", c.v, c.name, got)
		}
	}
	write(t, h, "etc/os-release", "ID=\"almalinux\"\nVERSION_ID=\"9.6\"\n")
	h.Env.Platform = platform.Detect(h.Env.Root)
	h.Env.M = h.Env.Platform.Managed()
	if !ownsPackage(h.Env, "8.3", "php83-php-fpm") || !ownsPackage(h.Env, "8.3", "php83-runtime") || ownsPackage(h.Env, "8.3", "php84-php-fpm") || ownsPackage(h.Env, "8.3", "php83x-php") {
		t.Error("remi package ownership")
	}
}

func TestPHPUpdateUpgradesOnlyThatVersionAndReloads(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "apt-get" && len(args) > 0 && args[0] == "-s" {
			return pendingUpgrades, nil, true
		}
		return "", nil, false
	}
	res, err := call(t, r, "php.update", map[string]any{"version": "8.3"})
	if err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, h, res); j.Status != jobs.Succeeded {
		t.Fatalf("%s: %s", j.Status, j.Error)
	}
	if !h.Runner.Called("apt-get install --only-upgrade -y -q -o Dpkg::Options::=--force-confold -o DPkg::Lock::Timeout=300 -- php8.3-cli php8.3-fpm php8.3-redis") {
		t.Errorf("calls: %v", h.Runner.Calls)
	}
	for _, c := range h.Runner.Calls {
		if strings.Contains(c, "libssl") || strings.Contains(c, "php8.4") {
			t.Errorf("another package was upgraded: %s", c)
		}
	}
	if !h.Runner.Called("php-fpm8.3 -t") {
		t.Errorf("PHP-FPM not tested: %v", h.Runner.Calls)
	}
}

func TestPHPUpdateRefusals(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "apt-get" && len(args) > 0 && args[0] == "-s" {
			return "Inst libssl3t64 [1] (2 Ubuntu:24.04/noble-security [amd64])\n", nil, true
		}
		return "", nil, false
	}
	if _, err := call(t, r, "php.update", map[string]any{"version": "8.3"}); err == nil || !strings.Contains(err.Error(), "up to date") {
		t.Errorf("up to date: %v", err)
	}
	if _, err := call(t, r, "php.update", map[string]any{"version": "5.6"}); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("not installed: %v", err)
	}
	if _, err := call(t, r, "php.update", map[string]any{"version": "8.3;rm"}); err == nil {
		t.Error("bad version accepted")
	}
}
