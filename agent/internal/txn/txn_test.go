package txn

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func setup(t *testing.T) *testhost.Host {
	h := testhost.New(t)
	h.Env.Writer.Policy.Patterns = append(h.Env.Writer.Policy.Patterns, "/etc/demo/**", "/usr/local/lib/whacp/**")
	os.MkdirAll(filepath.Join(h.Base, "etc/demo/sub"), 0o755)
	os.MkdirAll(filepath.Join(h.Base, "usr/local/lib/whacp"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/demo/a.conf"), []byte("old a\n"), 0o640)
	os.Symlink("a.conf", filepath.Join(h.Base, "etc/demo/link.conf"))
	return h
}

// Everything a transaction did is undone: replaced files byte for byte
// with their mode, created files and folders removed, packages and
// accounts handed to the hooks, and the reason recorded.
func TestRollbackUndoesEverything(t *testing.T) {
	h := setup(t)
	tx, err := Begin(h.Env, "demo", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Snapshot(1<<20, "/etc/demo", "/etc/missing", "/etc/demo/sub", "/etc/demo"); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tx.WriteFile("/etc/demo/a.conf", []byte("new a\n"), 0o644, agentfs.RootOwner))
	must(tx.WriteFile("/etc/demo/sub/b.conf", []byte("b\n"), 0o644, agentfs.RootOwner))
	must(tx.MkdirAll("/usr/local/lib/whacp/mods/v1", 0o755, agentfs.RootOwner))
	src := filepath.Join(t.TempDir(), "mod.so")
	os.WriteFile(src, []byte("ELF…"), 0o644)
	must(tx.InstallFile("/usr/local/lib/whacp/mods/v1/mod.so", src, 0o644, agentfs.RootOwner))
	if err := tx.InstallFile("/etc/demo/a.conf", src, 0o644, agentfs.RootOwner); err == nil {
		t.Fatal("InstallFile replaced an existing file")
	}
	must(tx.Package("libdemo-dev"))
	must(tx.Account("whacp-build"))

	// The journal lists every change before the rollback.
	loaded, err := Load(h.Env, "demo", tx.ID)
	if err != nil || loaded.Status != Running || len(loaded.Changes) != 7 {
		t.Fatalf("%v %+v", err, loaded)
	}
	var removedPkgs []string
	var removedAcct string
	after := 0
	err = tx.Rollback(context.Background(), Hooks{
		RemovePackages: func(_ context.Context, n []string) error { removedPkgs = n; return nil },
		RemoveAccount:  func(_ context.Context, n string) error { removedAcct = n; return nil },
		AfterRestore:   func(context.Context) error { after++; return nil },
	}, errors.New("nginx -t failed"))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(h.Base, "etc/demo/a.conf")); string(b) != "old a\n" {
		t.Fatalf("a.conf = %q", b)
	}
	if info, _ := os.Stat(filepath.Join(h.Base, "etc/demo/a.conf")); info.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", info.Mode())
	}
	for _, gone := range []string{"etc/demo/sub/b.conf", "usr/local/lib/whacp/mods/v1/mod.so", "usr/local/lib/whacp/mods"} {
		if _, err := os.Lstat(filepath.Join(h.Base, gone)); !os.IsNotExist(err) {
			t.Errorf("%s left", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(h.Base, "usr/local/lib/whacp")); err != nil {
		t.Error("a folder that existed before was removed")
	}
	if strings.Join(removedPkgs, ",") != "libdemo-dev" || removedAcct != "whacp-build" || after != 1 {
		t.Fatalf("hooks: %v %q %d", removedPkgs, removedAcct, after)
	}
	done, _ := Load(h.Env, "demo", tx.ID)
	if done.Status != RolledBack || done.Error != "nginx -t failed" || done.FinishedAt == nil {
		t.Fatalf("%+v", done)
	}
	// The snapshot kept the folder as it was, links included.
	snap := filepath.Join(h.Base, strings.TrimPrefix(tx.Dir(), "/"), "snapshot/etc/demo")
	if b, _ := os.ReadFile(filepath.Join(snap, "a.conf")); string(b) != "old a\n" {
		t.Fatalf("snapshot: %q", b)
	}
	if l, _ := os.Readlink(filepath.Join(snap, "link.conf")); l != "a.conf" {
		t.Fatalf("snapshot link: %q", l)
	}
	if len(done.Snapshots) != 1 {
		t.Fatalf("missing paths are skipped: %v", done.Snapshots)
	}
}

// A committed transaction can be rolled back later; its record stays.
func TestCommittedCanBeRolledBackLater(t *testing.T) {
	h := setup(t)
	tx, _ := Begin(h.Env, "demo", io.Discard)
	tx.WriteFile("/etc/demo/a.conf", []byte("v2\n"), 0o644, agentfs.RootOwner)
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	later, _ := Load(h.Env, "demo", tx.ID)
	if err := later.Rollback(context.Background(), Hooks{}, errors.New("administrator asked")); err != nil {
		t.Fatal(err)
	}
	if h.Read("/etc/demo/a.conf") != "old a\n" {
		t.Fatal("not restored")
	}
	rec, _ := Load(h.Env, "demo", tx.ID)
	if rec.Status != RolledBack || rec.RolledBackAt == nil {
		t.Fatalf("%+v", rec)
	}
}

// The agent stopped mid-way: the next start rolls it back.
func TestRecoverInterrupted(t *testing.T) {
	h := setup(t)
	tx, _ := Begin(h.Env, "demo", io.Discard)
	tx.WriteFile("/etc/demo/a.conf", []byte("half\n"), 0o644, agentfs.RootOwner)
	// (no commit, no rollback: the process "died")
	lines := Recover(context.Background(), h.Env, "demo", Hooks{}, io.Discard)
	if len(lines) != 1 || h.Read("/etc/demo/a.conf") != "old a\n" {
		t.Fatalf("%v %q", lines, h.Read("/etc/demo/a.conf"))
	}
	if again := Recover(context.Background(), h.Env, "demo", Hooks{}, io.Discard); len(again) != 0 {
		t.Fatal("recovered twice")
	}
}

// A failure while undoing is reported, and the others are still undone.
func TestRollbackReportsFailures(t *testing.T) {
	h := setup(t)
	tx, _ := Begin(h.Env, "demo", io.Discard)
	tx.WriteFile("/etc/demo/a.conf", []byte("x\n"), 0o644, agentfs.RootOwner)
	tx.Package("libx")
	err := tx.Rollback(context.Background(), Hooks{RemovePackages: func(context.Context, []string) error { return errors.New("apt locked") }}, errors.New("build failed"))
	if err == nil || !strings.Contains(err.Error(), "apt locked") {
		t.Fatalf("%v", err)
	}
	if h.Read("/etc/demo/a.conf") != "old a\n" {
		t.Fatal("files not restored")
	}
	if rec, _ := Load(h.Env, "demo", tx.ID); rec.Status != RollbackFailed {
		t.Fatalf("%+v", rec)
	}
}

// Old transactions go, the last known-good one stays, whatever its age.
func TestPruneKeepsTheLastKnownGood(t *testing.T) {
	h := setup(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	mk := func(daysAgo int, status string) string {
		h.Env.Now = func() time.Time { return now.AddDate(0, 0, -daysAgo) }
		tx, _ := Begin(h.Env, "demo", io.Discard)
		switch status {
		case Committed:
			tx.Commit()
		case RolledBack:
			tx.Rollback(context.Background(), Hooks{}, errors.New("x"))
		}
		return tx.ID
	}
	goodOld := mk(90, Committed)
	failedOld := mk(60, RolledBack)
	recent := mk(1, RolledBack)
	h.Env.Now = func() time.Time { return now }
	deleted := Prune(h.Env, "demo", 30*24*time.Hour)
	if len(deleted) != 1 || deleted[0] != failedOld {
		t.Fatalf("deleted %v", deleted)
	}
	for _, id := range []string{goodOld, recent} {
		if _, err := Load(h.Env, "demo", id); err != nil {
			t.Errorf("%s pruned", id)
		}
	}
	if _, err := Load(h.Env, "demo", "../../etc"); err == nil {
		t.Fatal("path in an id")
	}
}
