package database

import (
	"path/filepath"
	"testing"
)

// TestFreshInstallAppliesAllMigrations proves a brand-new database gets the
// full schema from the embedded migrations (same names as the PHP panel).
func TestFreshInstallAppliesAllMigrations(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "whacp.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ran, err := Migrate(db)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(ran) != 58 {
		t.Fatalf("expected 58 migrations applied on a fresh DB, got %d", len(ran))
	}
	// The first and a late migration must both be present and ordered.
	if ran[0] != "0001_initial_schema" || ran[len(ran)-1] != "0058_revoked_sessions" {
		t.Fatalf("unexpected order: first=%s last=%s", ran[0], ran[len(ran)-1])
	}
	// Core tables exist.
	for _, table := range []string{"users", "roles", "websites", "plans", "feature_lists", "mongo_remotes", "migrations"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing (n=%d, err=%v)", table, n, err)
		}
	}
	// Running again is a no-op.
	again, err := Migrate(db)
	if err != nil || len(again) != 0 {
		t.Fatalf("second migrate should be a no-op, ran %d (err=%v)", len(again), err)
	}
}

// TestExistingPhpDatabaseIsNotReapplied proves upgrade compatibility: when the
// migrations ledger already lists the names (as a PHP install would), the Go
// migrator applies nothing and does not touch existing data.
func TestExistingPhpDatabaseIsNotReapplied(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	all, err := migrations()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := ensureTable(db); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// Simulate a PHP-created DB: every migration name already recorded.
	for _, m := range all {
		if _, err := db.Exec(`INSERT INTO migrations (name, applied_at) VALUES (?, 'x')`, m.Name); err != nil {
			t.Fatalf("seed %s: %v", m.Name, err)
		}
	}
	ran, err := Migrate(db)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(ran) != 0 {
		t.Fatalf("expected no migrations on an up-to-date DB, got %v", ran)
	}
	pending, err := Pending(db)
	if err != nil || len(pending) != 0 {
		t.Fatalf("expected no pending, got %v (err=%v)", pending, err)
	}
}
