package sites

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// A site of a resource-limited account gets its own FPM master in the
// account's slice; lifting the limits moves it back.
func TestLimitedAccountGetsOwnFPMMaster(t *testing.T) {
	h := testhost.New(t)
	m := &Manager{Env: h.Env}
	ctx := context.Background()
	if _, err := m.Apply(ctx, spec()); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(h.Base, h.Env.Cfg.StateDir, "limits.json")
	os.MkdirAll(filepath.Dir(state), 0o755)
	os.WriteFile(state, []byte(`{"accounts":{"acct3":{"limits":{"cpu_percent":100},"users":["newsite"]}}}`), 0o600)

	if _, err := m.Apply(ctx, spec()); err != nil {
		t.Fatal(err)
	}
	a := AccountFPM{User: "newsite", Version: "8.3"}
	if h.Exists("etc/php/8.3/fpm/pool.d/whacp-new.test.conf") {
		t.Fatal("pool still in the shared master")
	}
	pool := h.Read(strings.TrimPrefix(filepath.Join(a.PoolDir(), "whacp-new.test.conf"), "/"))
	if !strings.Contains(pool, "listen = /run/php/whacp-new.test.sock") {
		t.Fatalf("pool:\n%s", pool)
	}
	unit := h.Read(strings.TrimPrefix(filepath.Join(h.Env.M.UnitDir, a.Unit()), "/"))
	for _, want := range []string{"Slice=whacp-acct3.slice", "--fpm-config /etc/whacp-fpm/newsite-8.3/php-fpm.conf", "--nodaemonize"} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	for _, c := range []string{"php-fpm8.3 -t -y /etc/whacp-fpm/newsite-8.3/php-fpm.conf", "systemctl reload -- php8.3-fpm.service", "systemctl enable -- " + a.Unit()} {
		if !h.Runner.Called(c) {
			t.Errorf("expected %q; calls=%v", c, h.Runner.Calls)
		}
	}
	// Limits lifted: back to the shared master; the account's master goes.
	os.WriteFile(state, []byte(`{"accounts":{}}`), 0o600)
	if _, err := m.Apply(ctx, spec()); err != nil {
		t.Fatal(err)
	}
	if !h.Exists("etc/php/8.3/fpm/pool.d/whacp-new.test.conf") || h.Exists(strings.TrimPrefix(filepath.Join(h.Env.M.UnitDir, a.Unit()), "/")) {
		t.Fatal("not moved back")
	}
	if !h.Runner.Called("systemctl disable -- " + a.Unit()) {
		t.Fatalf("master not disabled; calls=%v", h.Runner.Calls)
	}
}
