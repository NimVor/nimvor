package sites

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestFPMStatusIsOptInAndReversible(t *testing.T) {
	h := testhost.New(t)
	m := &Manager{Env: h.Env}
	ctx := context.Background()
	if _, err := m.Apply(ctx, spec()); err != nil {
		t.Fatal(err)
	}
	const pool = "etc/php/8.3/fpm/pool.d/nimvor-new.test.conf"
	original := h.Read(pool)
	if strings.Contains(original, "pm.status_path") || FPMStatusEnabled(h.Env) {
		t.Fatal("the status page must be off until an administrator switches it on")
	}
	if r := m.fpmStatusReport(); r.Enabled || r.PHPSites != 1 || r.WithStatus != 0 {
		t.Fatalf("report %+v", r)
	}

	// On: one line is added to the pool, validated and reloaded; nothing else changes.
	h.Runner.Calls = nil
	r, err := m.setFPMStatus(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Enabled || r.WithStatus != 1 || len(r.Skipped) != 0 {
		t.Fatalf("report %+v", r)
	}
	on := h.Read(pool)
	if !strings.Contains(on, "pm.status_path = /nimvor-status\n") || strings.Replace(on, "pm.status_path = /nimvor-status\n", "", 1) != original {
		t.Fatalf("exactly one line must be added:\n%s", on)
	}
	for _, c := range []string{"php-fpm8.3 -t", "systemctl reload -- php8.3-fpm.service"} {
		if !h.Runner.Called(c) {
			t.Errorf("expected %q; calls=%v", c, h.Runner.Calls)
		}
	}
	// A website saved later keeps the line (the flag is part of the layout), and a repeat changes nothing.
	if _, err := m.Apply(ctx, spec()); err != nil {
		t.Fatal(err)
	}
	if h.Read(pool) != on {
		t.Error("saving the website dropped the status page")
	}
	h.Runner.Calls = nil
	if _, err := m.setFPMStatus(ctx, true); err != nil || h.Runner.Called("systemctl reload -- php8.3-fpm.service") {
		t.Errorf("a repeat must not reload anything: %v %v", err, h.Runner.Calls)
	}
	// The php.ini watcher does not take the new pool for a hand-edited one.
	if err := m.reconcilePHP(ctx, "new.test", true); err != nil {
		t.Fatal(err)
	}
	if st := m.loadStatus("new.test"); st.Error != "" {
		t.Errorf("reconcile: %s", st.Error)
	}

	// Off: back to the original, byte for byte.
	if r, err = m.setFPMStatus(ctx, false); err != nil || r.Enabled || r.WithStatus != 0 {
		t.Fatalf("off: %+v %v", r, err)
	}
	if h.Read(pool) != original {
		t.Errorf("turning it off must restore the pool exactly:\n%s", h.Read(pool))
	}
}

func TestFPMStatusLeavesHandEditedPoolsAndRollsBack(t *testing.T) {
	h := testhost.New(t)
	m := &Manager{Env: h.Env}
	ctx := context.Background()
	if _, err := m.Apply(ctx, spec()); err != nil {
		t.Fatal(err)
	}
	const pool = "etc/php/8.3/fpm/pool.d/nimvor-new.test.conf"
	edited := h.Read(pool) + "; edited by hand\n"
	os.WriteFile(filepath.Join(h.Base, pool), []byte(edited), 0o644)
	r, err := m.setFPMStatus(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Skipped) != 1 || !strings.Contains(r.Skipped[0], "changed by hand") || h.Read(pool) != edited {
		t.Fatalf("a hand-edited pool must be left alone and listed: %+v\n%s", r, h.Read(pool))
	}

	// php-fpm -t refuses: nothing is written and the setting stays off.
	os.Remove(filepath.Join(h.Base, fpmStatusFile(h.Env))) // back to "off" (the run above switched it on, leaving the hand-edited pool alone)
	os.WriteFile(filepath.Join(h.Base, pool), []byte(strings.TrimSuffix(edited, "; edited by hand\n")), 0o644)
	h.Fail["php-fpm8.3 -t"] = "configuration error"
	before := h.Read(pool)
	if _, err := m.setFPMStatus(ctx, true); err == nil {
		t.Fatal("a refused configuration must be an error")
	}
	delete(h.Fail, "php-fpm8.3 -t")
	if h.Read(pool) != before || FPMStatusEnabled(h.Env) {
		t.Errorf("the refusal must leave the pool and the setting as they were:\n%s", h.Read(pool))
	}
}
