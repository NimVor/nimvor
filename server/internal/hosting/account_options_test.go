package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
)

func TestAccountOptionsDefaults(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	store := NewAccountOptionStore(h.db)

	def, err := store.Get(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !def.DNSDKIM || !def.DNSSPF || !def.DNSDMARC || def.DNSRegistrarNS || def.DNSOverwrite ||
		def.MailRouting != "auto" || !def.SpamFilter || !def.SpamBox {
		t.Fatalf("defaults = %+v", def)
	}
	// A missing row yields the defaults too.
	got, err := store.Get(ctx, int64Ptr(999))
	if err != nil {
		t.Fatal(err)
	}
	if got != def {
		t.Fatalf("missing row = %+v, want defaults %+v", got, def)
	}
}

func TestAccountOptionsSetMerge(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	store := NewAccountOptionStore(h.db)
	id := int64(42)
	seedAccount(t, h.db, id)

	if err := store.Set(ctx, id, map[string]any{"mail_routing": "backup", "dns_dkim": "1"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, &id)
	if err != nil {
		t.Fatal(err)
	}
	if got.MailRouting != "backup" || !got.DNSDKIM || !got.DNSSPF || !got.SpamBox {
		t.Fatalf("merged = %+v, want backup routing on defaults", got)
	}

	// A partial update keeps the previous value.
	if err := store.Set(ctx, id, map[string]any{"dns_overwrite": true}); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Get(ctx, &id)
	if got.MailRouting != "backup" || !got.DNSOverwrite {
		t.Fatalf("second merge = %+v", got)
	}
}

func TestAccountOptionsSetCleaning(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	store := NewAccountOptionStore(h.db)
	id := int64(43)
	seedAccount(t, h.db, id)

	// Unknown mail routing falls back to auto; unknown keys are dropped;
	// a non-bool checkbox value is coerced.
	if err := store.Set(ctx, id, map[string]any{
		"mail_routing": "bogus", "dns_dkim": "1", "not_an_option": "yes", "dns_spf": false,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, &id)
	if err != nil {
		t.Fatal(err)
	}
	if got.MailRouting != "auto" || !got.DNSDKIM || got.DNSSPF {
		t.Fatalf("cleaned = %+v", got)
	}
	var raw string
	if err := h.db.QueryRowContext(ctx, "SELECT options FROM account_options WHERE user_id = ?", id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var blob map[string]any
	if err := json.Unmarshal([]byte(raw), &blob); err != nil {
		t.Fatal(err)
	}
	if _, ok := blob["not_an_option"]; ok {
		t.Fatalf("unknown key stored: %s", raw)
	}
}

func TestAccountOptionsStoredBlob(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	store := NewAccountOptionStore(h.db)
	id := int64(44)
	seedAccount(t, h.db, id)
	if err := store.Set(ctx, id, map[string]any{"dns_registrar_ns": true}); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := h.db.QueryRowContext(ctx, "SELECT options FROM account_options WHERE user_id = ?", id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	// The blob uses the PHP key names.
	var blob map[string]any
	if err := json.Unmarshal([]byte(raw), &blob); err != nil {
		t.Fatal(err)
	}
	if blob["dns_registrar_ns"] != true || blob["dns_dkim"] != true || blob["mail_routing"] != "auto" {
		t.Fatalf("blob = %s", raw)
	}
}

func int64Ptr(v int64) *int64 { return &v }

// seedAccount inserts a bare customer so account_options rows (which reference
// users, foreign keys enforced) have an owner.
func seedAccount(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users (id,username,email,password_hash,role_id,is_active,password_changed_at,created_at,updated_at)
		VALUES (?,?,?,'h',3,1,'','','')`, id, fmt.Sprintf("acct%d", id), fmt.Sprintf("acct%d@x", id)); err != nil {
		t.Fatal(err)
	}
}
