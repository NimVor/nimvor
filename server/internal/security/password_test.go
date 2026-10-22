package security

import (
	"context"
	"database/sql"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// Strength vectors computed from PasswordPolicy::strength:
// min(100, floor(weightedLength * log2(max(2, pool)))).
func TestStrengthVectors(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"aaaaaaaaaaaaaaaa", 9},  // pool 26, weighted length 2 (one distinct rune)
		{"abcdefghij1234", 62},   // pool 36, 12 distinct
		{"Abcdefghij1234!", 98},  // pool 95, 15 distinct
		{"Aa1!Aa1!Aa1!Aa1!Aa1!", 52}, // pool 95, weighted length 8 (4 distinct)
	}
	for _, c := range cases {
		if got := Strength(c.in); got != c.want {
			t.Errorf("Strength(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestWeakness(t *testing.T) {
	if got := Weakness("abcdefghijk", ""); got != "The password must be at least 12 characters long." {
		t.Errorf("short = %q", got)
	}
	long := "a1A!a1A!a1A!a1A!a1A!a1A!"
	if len(long) < 1025 {
		t.Fatal("test string must exceed MAX_LENGTH")
	}
	if got := Weakness(long, ""); got != "The password is too long." {
		t.Errorf("long = %q", got)
	}
	if got := Weakness("SuperRoot123!secret", "root"); got != "The password must not contain the username." {
		t.Errorf("username = %q", got)
	}
	if got := Weakness("aaaaaaaaaaab", ""); got != "The password is too repetitive." {
		t.Errorf("repetitive = %q", got)
	}
	if got := Weakness("correct-horse-battery", "nobody"); got != "" {
		t.Errorf("ok = %q", got)
	}
}

func policyDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func setSetting(t *testing.T, db *sql.DB, key, json string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, '')`, key, json); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyCheckDefaults(t *testing.T) {
	db := policyDB(t)
	p := NewPolicy(settings.NewRepository(db))
	ctx := context.Background()

	// Base rules always apply, even before the configured strength.
	if got, err := p.Check(ctx, "abcdefghijk", ""); err != nil || got != "The password must be at least 12 characters long." {
		t.Errorf("short = %q, %v", got, err)
	}
	if got, err := p.Check(ctx, "SuperRoot123!secret", "root"); err != nil || got != "The password must not contain the username." {
		t.Errorf("username = %q, %v", got, err)
	}

	// Default policy (enabled, min strength 65): 62 < 65.
	weak := "The password is too weak (strength 62, at least 65 is required). Make it longer or mix upper and lower case, digits and symbols."
	if got, err := p.Check(ctx, "abcdefghij1234", ""); err != nil || got != weak {
		t.Errorf("weak = %q, %v; want %q", got, err, weak)
	}
	// 17 distinct mixed-class runes: strength 100, accepted.
	if got, err := p.Check(ctx, "Abcdefghijkl1234!", ""); err != nil || got != "" {
		t.Errorf("strong = %q, %v; want ok", got, err)
	}
}

func TestPolicyCheckConfiguredRules(t *testing.T) {
	db := policyDB(t)
	p := NewPolicy(settings.NewRepository(db))
	ctx := context.Background()

	// Raise the length floor above the base 12.
	setSetting(t, db, "password_min_length", `16`)
	if got, err := p.Check(ctx, "Abcdefghij1234!", ""); err != nil || got != "The password must be at least 16 characters long." {
		t.Errorf("length = %q, %v", got, err)
	}
	if got, err := p.Check(ctx, "Abcdefghijkl1234!", ""); err != nil || got != "" {
		t.Errorf("long enough = %q, %v", got, err)
	}

	// Character-count rule (17 mixed-class runes, zero digits).
	setSetting(t, db, "password_min_numeric", `3`)
	if got, err := p.Check(ctx, "Abcdefghijklmnop!", ""); err != nil || got != "The password needs at least 3 numeric characters." {
		t.Errorf("numeric = %q, %v", got, err)
	}

	// Policy off: only the base rules remain (a 12-char password that would
	// fail the strength floor passes).
	setSetting(t, db, "password_min_length", `12`)
	setSetting(t, db, "password_min_numeric", `0`)
	setSetting(t, db, "policy_password_strength", `false`)
	if got, err := p.Check(ctx, "abcdefghij1234", ""); err != nil || got != "" {
		t.Errorf("policy off = %q, %v; want ok", got, err)
	}
	if got, err := p.Check(ctx, "SuperRoot123!secret", "root"); err != nil || got != "The password must not contain the username." {
		t.Errorf("base still on = %q, %v", got, err)
	}
}
