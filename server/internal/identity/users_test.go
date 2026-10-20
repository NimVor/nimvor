package identity

import (
	"context"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

func TestUserRepositoryAuthAndLookup(t *testing.T) {
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO roles (id,name,label,scope,require_two_factor,created_at) VALUES (1,'administrator','Admin','all',1,'')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,session_epoch)
		VALUES (1,'Root','root@example.test','Root User','HASH',1,1,'2026-01-01T00:00:00Z','','',0)`); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db)
	ctx := context.Background()

	// findForAuth matches by username OR email, case-insensitively.
	for _, login := range []string{"Root", "root", "ROOT@EXAMPLE.TEST"} {
		a, err := repo.FindForAuth(ctx, login)
		if err != nil {
			t.Fatalf("FindForAuth(%q): %v", login, err)
		}
		if a.PasswordHash != "HASH" || a.ID != 1 {
			t.Fatalf("FindForAuth(%q) = %+v", login, a)
		}
	}
	if _, err := repo.FindForAuth(ctx, "nobody"); err != ErrNotFound {
		t.Fatalf("missing user err = %v, want ErrNotFound", err)
	}

	// Find joins the role and exposes scope + 2FA requirement.
	u, err := repo.Find(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if u.RoleScope != "all" || !u.RoleRequires2FA || u.DisplayName != "Root User" {
		t.Fatalf("Find = %+v", u)
	}
	if !CanAct(u) {
		t.Fatal("active, unsuspended user must CanAct")
	}

	// Session epoch bump changes the value (used in the session fingerprint).
	e0, _ := repo.SessionEpoch(ctx, 1)
	if err := repo.BumpSessionEpoch(ctx, 1); err != nil {
		t.Fatal(err)
	}
	e1, _ := repo.SessionEpoch(ctx, 1)
	if e1 != e0+1 {
		t.Fatalf("epoch %d -> %d, want +1", e0, e1)
	}

	// PasswordHash + UpdatePassword round trip.
	if h, _ := repo.PasswordHash(ctx, 1); h != "HASH" {
		t.Fatalf("PasswordHash = %q", h)
	}
	if err := repo.UpdatePassword(ctx, 1, "NEWHASH"); err != nil {
		t.Fatal(err)
	}
	if h, _ := repo.PasswordHash(ctx, 1); h != "NEWHASH" {
		t.Fatalf("after update PasswordHash = %q", h)
	}

	// RecordLogin stamps the IP.
	if err := repo.RecordLogin(ctx, 1, "203.0.113.7"); err != nil {
		t.Fatal(err)
	}
	u2, _ := repo.Find(ctx, 1)
	if !u2.LastLoginIP.Valid || u2.LastLoginIP.String != "203.0.113.7" {
		t.Fatalf("last_login_ip = %+v", u2.LastLoginIP)
	}

	// Inactive user cannot act.
	if _, err := db.Exec(`UPDATE users SET is_active = 0 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	u3, _ := repo.Find(ctx, 1)
	if CanAct(u3) {
		t.Fatal("inactive user must not CanAct")
	}
}

func TestCanActNil(t *testing.T) {
	if CanAct(nil) {
		t.Fatal("nil user must not CanAct")
	}
}
