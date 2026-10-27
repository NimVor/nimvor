package audit

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

func TestRecordInsertsRowAndRedacts(t *testing.T) {
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	// audit_logs.user_id references users(id); seed the actor.
	if _, err := db.Exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES (1,'administrator','Admin','all','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id,username,email,password_hash,role_id,is_active,password_changed_at,created_at,updated_at)
		VALUES (7,'bob','b@x','h',1,1,'','','')`); err != nil {
		t.Fatal(err)
	}
	l := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	uid := int64(7)
	l.Record(context.Background(), Entry{
		UserID: &uid, Username: "bob", Action: "database.create",
		TargetType: "database", TargetID: "bob_shop", IP: "203.0.113.1",
		Details: map[string]any{"name": "bob_shop", "password": "hunter2", "nested": map[string]any{"api_key": "abc", "ok": "keep"}},
	})

	var action, result, username, details string
	var userID int64
	err = db.QueryRow(`SELECT user_id, username, action, result, details FROM audit_logs`).Scan(&userID, &username, &action, &result, &details)
	if err != nil {
		t.Fatal(err)
	}
	if userID != 7 || username != "bob" || action != "database.create" || result != "success" {
		t.Fatalf("row = %d/%s/%s/%s", userID, username, action, result)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(details), &d); err != nil {
		t.Fatalf("details not JSON: %v", err)
	}
	if d["name"] != "bob_shop" {
		t.Fatalf("name redacted unexpectedly: %v", d["name"])
	}
	if d["password"] != Placeholder {
		t.Fatalf("password not redacted: %v", d["password"])
	}
	nested, _ := d["nested"].(map[string]any)
	if nested["api_key"] != Placeholder || nested["ok"] != "keep" {
		t.Fatalf("nested redaction wrong: %v", nested)
	}
}

func TestRecordDefaultsResultAndEmptyDetails(t *testing.T) {
	db, _ := database.Open(":memory:")
	defer db.Close()
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	New(db, slog.New(slog.NewTextHandler(io.Discard, nil))).Record(context.Background(), Entry{Action: "x"})
	var result, details string
	if err := db.QueryRow(`SELECT result, details FROM audit_logs`).Scan(&result, &details); err != nil {
		t.Fatal(err)
	}
	if result != "success" || details != "{}" {
		t.Fatalf("defaults wrong: result=%q details=%q", result, details)
	}
}

// The scheduler and the command line act as a system user with id 0 and no
// users row: the entry is written without a user instead of failing the
// foreign key.
func TestRecordSystemActorHasNoUserRow(t *testing.T) {
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	l := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	zero := int64(0)
	l.Record(context.Background(), Entry{UserID: &zero, Username: "scheduler", Action: "backup.scheduled", TargetType: "backup_schedule", TargetID: "1"})
	var n int
	var uid *int64
	if err := db.QueryRow(`SELECT COUNT(*), MAX(user_id) FROM audit_logs WHERE username = 'scheduler'`).Scan(&n, &uid); err != nil || n != 1 || uid != nil {
		t.Fatalf("rows %d user %v err %v", n, uid, err)
	}
}
