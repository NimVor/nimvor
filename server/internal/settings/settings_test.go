package settings

import (
	"context"
	"database/sql"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

func newTestRepo(t *testing.T) (*Repository, *sql.DB) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return NewRepository(db), db
}

func set(t *testing.T, db *sql.DB, key, json string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, '')`, key, json); err != nil {
		t.Fatal(err)
	}
}

func TestAccessorsDefaultsWhenAbsent(t *testing.T) {
	r, _ := newTestRepo(t)
	ctx := context.Background()
	if s, err := r.String(ctx, "panel_url", ""); err != nil || s != "" {
		t.Errorf("String(absent) = %q, %v; want empty, nil", s, err)
	}
	if n, err := r.Int(ctx, "mail.port", 587); err != nil || n != 587 {
		t.Errorf("Int(absent) = %d, %v; want 587, nil", n, err)
	}
	if b, err := r.Bool(ctx, "policy_password_strength", true); err != nil || !b {
		t.Errorf("Bool(absent) = %v, %v; want true, nil", b, err)
	}
}

func TestAccessorsDecodeJSON(t *testing.T) {
	r, db := newTestRepo(t)
	ctx := context.Background()
	set(t, db, "panel_name", `"WhaCP"`)
	set(t, db, "mail.port", `587`)
	set(t, db, "policy_password_strength", `true`)
	set(t, db, "hulk_enabled", `false`)

	if s, err := r.String(ctx, "panel_name", ""); err != nil || s != "WhaCP" {
		t.Errorf("String = %q, %v; want WhaCP", s, err)
	}
	if n, err := r.Int(ctx, "mail.port", 0); err != nil || n != 587 {
		t.Errorf("Int = %d, %v; want 587", n, err)
	}
	if b, err := r.Bool(ctx, "policy_password_strength", false); err != nil || !b {
		t.Errorf("Bool = %v, %v; want true", b, err)
	}
	if b, err := r.Bool(ctx, "hulk_enabled", true); err != nil || b {
		t.Errorf("Bool(false) = %v, %v; want false", b, err)
	}
}

func TestAccessorsFallBackOnMalformed(t *testing.T) {
	r, db := newTestRepo(t)
	ctx := context.Background()
	set(t, db, "mail.port", `"not-a-number"`)
	set(t, db, "hulk_enabled", `"yes"`)
	if n, err := r.Int(ctx, "mail.port", 587); err != nil || n != 587 {
		t.Errorf("Int(malformed) = %d, %v; want default 587", n, err)
	}
	if b, err := r.Bool(ctx, "hulk_enabled", false); err != nil || b {
		t.Errorf("Bool(malformed) = %v, %v; want default false", b, err)
	}
}
