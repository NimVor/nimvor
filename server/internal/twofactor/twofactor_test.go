package twofactor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// RFC 6238 appendix B, SHA-1 column. The test secret is the ASCII
// "12345678901234567890" (the RFC's 80-bit key, extended to 160 bits with a
// zero byte row — the RFC key is the 20-byte "12345678901234567890").
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // base32("12345678901234567890")

func TestTOTPRFC6238Vectors(t *testing.T) {
	// T (unix seconds) -> 8-digit code from RFC 6238 B.1 (SHA-1).
	vectors := []struct {
		t    int64
		code string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	if got, err := Base32Decode(rfcSecret); err != nil || string(got) != "12345678901234567890" {
		t.Fatalf("Base32Decode(rfcSecret) = %q, %v", got, err)
	}
	for _, v := range vectors {
		got, err := Code(rfcSecret, v.t/Period)
		if err != nil {
			t.Fatal(err)
		}
		if want := v.code[2:]; got != want {
			t.Errorf("Code(step %d) = %s, want %s (RFC 6238 %s)", v.t/Period, got, want, v.code)
		}
	}
}

func TestBase32RoundTripAndVectors(t *testing.T) {
	cases := []string{"hello", "hello?", "HELLO", "Subject: RFC 4648 test vectors", "12345678901234567890", ""}
	for _, in := range cases {
		enc := Base32Encode([]byte(in))
		dec, err := Base32Decode(enc)
		if err != nil || string(dec) != in {
			t.Errorf("round trip %q -> %q -> %q, %v", in, enc, dec, err)
		}
	}
	if enc := Base32Encode([]byte("hello")); enc != "NBSWY3DP" {
		t.Errorf("Base32Encode(hello) = %q, want NBSWY3DP", enc)
	}
	if _, err := Base32Decode("!!!"); err == nil {
		t.Error("Base32Decode(!!!) must fail")
	}
	// Case-insensitive, spaces and padding tolerated.
	if _, err := Base32Decode("nbswY3dP"); err != nil {
		t.Errorf("case-insensitive decode failed: %v", err)
	}
}

func TestMatchStepWindow(t *testing.T) {
	secret := rfcSecret
	now := int64(1234567890)
	stepNow := now / Period
	codeNow, _ := Code(secret, stepNow)
	codeNext, _ := Code(secret, stepNow+1)
	codePrev, _ := Code(secret, stepNow-1)

	if got := MatchStep(secret, codeNow, 0, now); got != stepNow {
		t.Errorf("current step not matched: %d", got)
	}
	if got := MatchStep(secret, codeNext, 0, now); got != stepNow+1 {
		t.Errorf("next step not matched: %d", got)
	}
	if got := MatchStep(secret, codePrev, 0, now); got != stepNow-1 {
		t.Errorf("previous step not matched: %d", got)
	}
	// A step at or below [after] (already used) is never accepted again.
	if got := MatchStep(secret, codePrev, stepNow, now); got != 0 {
		t.Errorf("old step accepted despite after=%d: %d", stepNow, got)
	}
	if got := MatchStep(secret, codeNow, stepNow, now); got != 0 {
		t.Errorf("current step accepted despite after=%d: %d", stepNow, got)
	}
	// Whitespace inside the code is ignored.
	if got := MatchStep(secret, " "+codeNow+" ", 0, now); got != stepNow {
		t.Errorf("whitespace-padded code not matched: %d", got)
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "12345a", "12 34 56"} {
		if got := MatchStep(secret, bad, 0, now); got != 0 {
			t.Errorf("MatchStep(%q) = %d, want 0", bad, got)
		}
	}
}

var recoveryRe = regexp.MustCompile(`^[a-z0-9]{5}-[a-z0-9]{5}$`)

func TestRecoveryCodesFormat(t *testing.T) {
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != recoveryCount || len(hashes) != recoveryCount {
		t.Fatalf("got %d codes / %d hashes", len(codes), len(hashes))
	}
	seen := map[string]bool{}
	for i, c := range codes {
		if !recoveryRe.MatchString(c) {
			t.Fatalf("code %d %q has wrong format", i, c)
		}
		if seen[c] {
			t.Fatalf("duplicate recovery code %q", c)
		}
		seen[c] = true
		raw := strings.ReplaceAll(c, "-", "")
		sum := sha256.Sum256([]byte(raw))
		if hex.EncodeToString(sum[:]) != hashes[i] {
			t.Fatalf("hash %d does not match code %q", i, c)
		}
	}
}

// ---- database-backed behaviour -------------------------------------------

var clk = int64(1_700_000_000)

func nextNow() int64 { return clk }

// setupDB migrates a fresh in-memory database with one user and enables
// two-factor for it at the fake clock. It returns the db, the service and
// the enabled secret.
func setupDB(t *testing.T) (*Service, string) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES (1,'administrator','Admin','all','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id,username,email,password_hash,role_id,is_active,password_changed_at,created_at,updated_at)
		VALUES (1,'root','root@example.test','HASH',1,1,'2026-01-01T00:00:00Z','','')`); err != nil {
		t.Fatal(err)
	}
	var key [32]byte
	svc := New(db, crypto.NewWithKey(key)).WithClock(nextNow)
	ctx := context.Background()
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	code, err := Code(secret, clk/Period)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Enable(ctx, 1, secret, code); err != nil {
		t.Fatal(err)
	}
	return svc, secret
}

func TestEnableStoresSealedSecretAndRejectsBadCode(t *testing.T) {
	clk = 1_700_000_000
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES (1,'administrator','Admin','all','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id,username,email,password_hash,role_id,is_active,password_changed_at,created_at,updated_at)
		VALUES (1,'root','root@example.test','HASH',1,1,'','')`); err != nil {
		t.Fatal(err)
	}
	var key [32]byte
	c := crypto.NewWithKey(key)
	svc := New(db, c).WithClock(nextNow)
	ctx := context.Background()

	if _, err := svc.Enable(ctx, 1, rfcSecret, "000000"); err != ErrBadCode {
		t.Fatalf("Enable with a bad code = %v, want ErrBadCode", err)
	}
	enabled, _ := svc.Enabled(ctx, 1)
	if enabled {
		t.Fatal("no row may exist after a failed Enable")
	}

	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	code, err := Code(secret, clk/Period)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := svc.Enable(ctx, 1, secret, code)
	if err != nil || len(codes) != recoveryCount {
		t.Fatalf("Enable = %v, %d codes", err, len(codes))
	}
	// The secret must be stored sealed (v1: prefix), never in the clear.
	var stored string
	if err := db.QueryRow(`SELECT secret FROM user_two_factor WHERE user_id = 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, "v1:") || strings.Contains(stored, secret) {
		t.Fatalf("secret not stored sealed: %q", stored)
	}
	if plain, err := c.Decrypt(stored); err != nil || string(plain) != secret {
		t.Fatalf("sealed secret does not decrypt back: %v", err)
	}
	// Enable is an upsert: enabling twice leaves exactly one row.
	code, _ = Code(secret, clk/Period)
	if _, err := svc.Enable(ctx, 1, secret, code); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_two_factor WHERE user_id = 1`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("row count after re-enable = %d, %v; want 1", n, err)
	}
}

func TestVerifyTotpRejectsReusedStep(t *testing.T) {
	svc, secret := setupDB(t)
	ctx := context.Background()
	code, err := Code(secret, clk/Period)
	if err != nil {
		t.Fatal(err)
	}
	if m, err := svc.Verify(ctx, 1, code); err != nil || m != "totp" {
		t.Fatalf("Verify = %q, %v; want totp", m, err)
	}
	// The same step was already used: the identical code must not work again.
	if _, err := svc.Verify(ctx, 1, code); err != ErrBadCode {
		t.Fatalf("reused step Verify err = %v, want ErrBadCode", err)
	}
	// The next step is still accepted.
	clk += Period
	code, _ = Code(secret, clk/Period)
	if m, err := svc.Verify(ctx, 1, code); err != nil || m != "totp" {
		t.Fatalf("next step Verify = %q, %v", m, err)
	}
}

func TestVerifyRecoveryCodeIsSingleUse(t *testing.T) {
	clk = 1_700_000_000
	svc, _ := setupDB(t)
	ctx := context.Background()
	codes, err := svc.RegenerateRecovery(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	status, err := svc.Status(ctx, 1)
	if err != nil || status == nil || status.RecoveryLeft != recoveryCount {
		t.Fatalf("status after regenerate = %+v, %v", status, err)
	}
	if m, err := svc.Verify(ctx, 1, codes[0]); err != nil || m != "recovery" {
		t.Fatalf("recovery Verify = %q, %v", m, err)
	}
	// The used code is dead.
	if _, err := svc.Verify(ctx, 1, codes[0]); err != ErrBadCode {
		t.Fatalf("reused recovery code err = %v, want ErrBadCode", err)
	}
	if _, err := svc.Verify(ctx, 1, codes[0][:5]); err != ErrBadCode {
		t.Fatalf("truncated recovery code err = %v, want ErrBadCode", err)
	}
	status, _ = svc.Status(ctx, 1)
	if status.RecoveryLeft != recoveryCount-1 {
		t.Fatalf("recovery left = %d, want %d", status.RecoveryLeft, recoveryCount-1)
	}
	// Dashes and case do not matter for a recovery code.
	if m, err := svc.Verify(ctx, 1, strings.ToUpper(strings.ReplaceAll(codes[1], "-", ""))); err != nil || m != "recovery" {
		t.Fatalf("normalized recovery Verify = %q, %v", m, err)
	}
}

func TestDisableAndStatus(t *testing.T) {
	svc, _ := setupDB(t)
	ctx := context.Background()
	status, err := svc.Status(ctx, 1)
	if err != nil || status == nil || status.RecoveryLeft != recoveryCount || status.EnabledAt == "" {
		t.Fatalf("status = %+v, %v", status, err)
	}
	if err := svc.Disable(ctx, 1); err != nil {
		t.Fatal(err)
	}
	enabled, _ := svc.Enabled(ctx, 1)
	if enabled {
		t.Fatal("still enabled after Disable")
	}
	if st, err := svc.Status(ctx, 1); err != nil || st != nil {
		t.Fatalf("Status after Disable = %+v, %v; want nil", st, err)
	}
	if _, err := svc.Verify(ctx, 1, "123456"); err != ErrNotEnabled {
		t.Fatalf("Verify after Disable = %v, want ErrNotEnabled", err)
	}
	if _, err := svc.RegenerateRecovery(ctx, 1); err != ErrNotEnabled {
		t.Fatalf("RegenerateRecovery after Disable = %v, want ErrNotEnabled", err)
	}
}

func TestRegenerateRecoveryInvalidatesOldCodes(t *testing.T) {
	svc, _ := setupDB(t)
	ctx := context.Background()
	oldCodes, err := svc.RegenerateRecovery(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RegenerateRecovery(ctx, 1); err != nil {
		t.Fatal(err)
	}
	// An old recovery code no longer works.
	if _, err := svc.Verify(ctx, 1, oldCodes[2]); err != ErrBadCode {
		t.Fatalf("stale recovery code err = %v, want ErrBadCode", err)
	}
}

func TestURI(t *testing.T) {
	got := URI("SECRET", "root@example.test", "WhaCP")
	want := "otpauth://totp/WhaCP%3Aroot%40example.test?secret=SECRET&issuer=WhaCP&algorithm=SHA1&digits=6&period=30"
	if got != want {
		t.Fatalf("URI = %q, want %q", got, want)
	}
}
