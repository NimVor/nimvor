// Package twofactor implements two-factor sign-in: RFC 6238 TOTP (SHA-1,
// 6 digits, 30 seconds — the scheme every authenticator app supports) plus
// single-use recovery codes. It is the Go port of app/Security/TwoFactor.php:
//
//   - secrets are stored sealed with the panel key (internal/crypto, "v1:"
//     format byte-compatible with PHP);
//   - recovery codes exist in the database only as SHA-256 hashes;
//   - a code is accepted for the previous, current and next time step, and
//     never for a step that was already used (the last used step is
//     remembered in user_two_factor.last_step);
//   - a sign-in code is either an authenticator code or a recovery code,
//     and a recovery code is used up when consumed.
package twofactor

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
)

// Period is the TOTP time step in seconds (RFC 6238 default).
const Period = 30

const (
	b32           = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	digits        = 6
	recoveryCount = 10
)

// ErrBadCode is returned when a TOTP or recovery code does not match.
var ErrBadCode = errors.New("twofactor: invalid code")

// ErrNotEnabled is returned when the user has no two-factor row.
var ErrNotEnabled = errors.New("twofactor: not enabled")

// Status describes an enabled two-factor account.
type Status struct {
	EnabledAt    string
	RecoveryLeft int
}

// NewSecret returns a fresh 20-byte (160-bit) secret, base32-encoded.
func NewSecret() (string, error) {
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return Base32Encode(raw[:]), nil
}

// URI builds the otpauth:// URI understood by authenticator apps
// (TwoFactor::uri).
func URI(secret, account, issuer string) string {
	if issuer == "" {
		issuer = "WhaCP"
	}
	return "otpauth://totp/" + rawurlencode(issuer+":"+account) +
		"?secret=" + secret +
		"&issuer=" + rawurlencode(issuer) +
		"&algorithm=SHA1&digits=6&period=" + fmt.Sprint(Period)
}

// rawurlencode is PHP's rawurlencode: x-www-form-urlencoded with %20 for
// spaces (Go's url.QueryEscape would emit "+").
func rawurlencode(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// Code computes the 6-digit TOTP for the time step (RFC 6238, SHA-1,
// dynamic truncation). TwoFactor::code.
func Code(secret string, step int64) (string, error) {
	key, err := Base32Decode(secret)
	if err != nil {
		return "", err
	}
	mac := hmacSHA1(key, step)
	offset := mac[19] & 0x0f
	bin := uint32(mac[offset]&0x7f)<<24 |
		uint32(mac[offset+1])<<16 |
		uint32(mac[offset+2])<<8 |
		uint32(mac[offset+3])
	return fmt.Sprintf("%0*d", digits, bin%1000000), nil
}

func hmacSHA1(key []byte, step int64) []byte {
	h := hmac.New(sha1.New, key)
	var b [8]byte // pack('J', step): 64-bit big-endian unsigned
	for i := 7; i >= 0; i-- {
		b[i] = byte(step)
		step >>= 8
	}
	h.Write(b[:])
	return h.Sum(nil)
}

// MatchStep returns the time step (previous, current or next) whose code
// matches, restricted to steps after [after], or 0 when none matches.
// TwoFactor::matchStep.
func MatchStep(secret, code string, after, nowSec int64) int64 {
	code = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f' {
			return -1
		}
		return r
	}, code)
	if len(code) != digits {
		return 0
	}
	for i := 0; i < digits; i++ {
		if code[i] < '0' || code[i] > '9' {
			return 0
		}
	}
	now := nowSec / Period
	for _, step := range []int64{now - 1, now, now + 1} {
		if step <= after {
			continue
		}
		want, err := Code(secret, step)
		if err != nil {
			return 0
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step
		}
	}
	return 0
}

// Base32Encode is RFC 4648 base32 without padding (TwoFactor::base32Encode).
func Base32Encode(b []byte) string {
	out := make([]byte, 0, (len(b)*8+4)/5)
	acc, bits := 0, 0
	for _, x := range b {
		acc = acc<<8 | int(x)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out = append(out, b32[acc>>bits&0x1f])
			acc &= (1 << bits) - 1
		}
	}
	if bits > 0 {
		out = append(out, b32[acc<<(5-bits)&0x1f])
	}
	return string(out)
}

// Base32Decode is RFC 4648 base32 (case-insensitive, padding and spaces
// tolerated). TwoFactor::base32Decode.
func Base32Decode(s string) ([]byte, error) {
	s = strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	s = strings.TrimRight(s, "=")
	if s == "" {
		return nil, errors.New("twofactor: empty base32 input")
	}
	out := make([]byte, 0, len(s)*5/8)
	acc, bits := 0, 0
	for _, c := range s {
		v := strings.IndexRune(b32, c)
		if v < 0 {
			return nil, errors.New("twofactor: invalid base32 secret")
		}
		acc = acc<<5 | v
		bits += 5
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
			acc &= (1 << bits) - 1
		}
	}
	return out, nil
}

// newRecoveryCodes returns 10 fresh recovery codes ("xxxxx-xxxxx", 10 base32
// characters of entropy each) and their SHA-256 hashes (TwoFactor::recoveryCodes).
func newRecoveryCodes() ([]string, []string, error) {
	codes := make([]string, 0, recoveryCount)
	hashes := make([]string, 0, recoveryCount)
	for i := 0; i < recoveryCount; i++ {
		var raw [8]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, nil, err
		}
		s := strings.ToLower(Base32Encode(raw[:]))[:10]
		codes = append(codes, s[:5]+"-"+s[5:])
		hashes = append(hashes, sha256hex(s))
	}
	return codes, hashes, nil
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// normalizeRecovery lowercases and strips non-alphanumerics
// (strtolower(preg_replace('/[^A-Za-z0-9]/', ”, $code))).
func normalizeRecovery(code string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(code) {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Service performs two-factor operations against the database.
type Service struct {
	db     *sql.DB
	crypto *crypto.Crypto
	now    func() int64
}

// New builds the two-factor service.
func New(db *sql.DB, c *crypto.Crypto) *Service {
	return &Service{db: db, crypto: c, now: time.Now().Unix}
}

// WithClock returns a copy with a replaced clock (tests).
func (s *Service) WithClock(now func() int64) *Service {
	return &Service{db: s.db, crypto: s.crypto, now: now}
}

// Enabled reports whether the user has two-factor sign-in on.
func (s *Service) Enabled(ctx context.Context, userID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_two_factor WHERE user_id = ?", userID).Scan(&n)
	return n > 0, err
}

// Status returns the two-factor status, or nil when off.
func (s *Service) Status(ctx context.Context, userID int64) (*Status, error) {
	var codes, enabledAt string
	err := s.db.QueryRowContext(ctx, "SELECT recovery_codes, enabled_at FROM user_two_factor WHERE user_id = ?", userID).
		Scan(&codes, &enabledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var hashes []string
	if jerr := json.Unmarshal([]byte(codes), &hashes); jerr != nil {
		hashes = nil
	}
	return &Status{EnabledAt: enabledAt, RecoveryLeft: len(hashes)}, nil
}

// Enable turns two-factor on after the user proved the app works with a
// valid code. It returns the one-time recovery codes, or ErrBadCode.
// The secret is stored sealed; recovery codes only as hashes.
func (s *Service) Enable(ctx context.Context, userID int64, secret, code string) ([]string, error) {
	step := MatchStep(secret, code, 0, s.now())
	if step == 0 {
		return nil, ErrBadCode
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	enc, err := s.crypto.Encrypt([]byte(secret))
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(hashes)
	if err != nil {
		return nil, err
	}
	enabledAt := time.Unix(s.now(), 0).UTC().Format("2006-01-02T15:04:05+00:00")
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO user_two_factor (user_id, secret, recovery_codes, last_step, enabled_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			secret = excluded.secret,
			recovery_codes = excluded.recovery_codes,
			last_step = excluded.last_step,
			enabled_at = excluded.enabled_at`,
		userID, enc, string(b), step, enabledAt)
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// Disable turns two-factor off.
func (s *Service) Disable(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM user_two_factor WHERE user_id = ?", userID)
	return err
}

// RegenerateRecovery replaces all recovery codes; the old ones stop working.
// Returns the new codes, or ErrNotEnabled.
func (s *Service) RegenerateRecovery(ctx context.Context, userID int64) ([]string, error) {
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(hashes)
	if err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE user_two_factor SET recovery_codes = ? WHERE user_id = ?", string(b), userID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotEnabled
	}
	return codes, nil
}

// Verify checks a sign-in code for the user. It returns "totp" or
// "recovery", or ErrBadCode / ErrNotEnabled. The check and the use-up of a
// code happen in one transaction, so a code can never be applied twice
// concurrently (TwoFactor::verify).
func (s *Service) Verify(ctx context.Context, userID int64, code string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	var enc, codesJSON string
	var lastStep int64
	err = tx.QueryRowContext(ctx,
		"SELECT secret, recovery_codes, last_step FROM user_two_factor WHERE user_id = ?", userID).
		Scan(&enc, &codesJSON, &lastStep)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotEnabled
	}
	if err != nil {
		return "", err
	}

	plain, err := s.crypto.Decrypt(enc)
	if err != nil {
		return "", err
	}
	if step := MatchStep(string(plain), code, lastStep, s.now()); step > 0 {
		if _, err := tx.ExecContext(ctx, "UPDATE user_two_factor SET last_step = ? WHERE user_id = ?", step, userID); err != nil {
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return "totp", nil
	}

	norm := normalizeRecovery(code)
	if len(norm) == 10 {
		sum := sha256hex(norm)
		var hashes []string
		if jerr := json.Unmarshal([]byte(codesJSON), &hashes); jerr == nil {
			match := -1
			for i, h := range hashes {
				if subtle.ConstantTimeCompare([]byte(h), []byte(sum)) == 1 {
					match = i
					break
				}
			}
			if match >= 0 {
				kept := make([]string, 0, len(hashes)-1)
				for i, h := range hashes {
					if i != match {
						kept = append(kept, h)
					}
				}
				b, jerr := json.Marshal(kept)
				if jerr != nil {
					return "", jerr
				}
				if _, err := tx.ExecContext(ctx, "UPDATE user_two_factor SET recovery_codes = ? WHERE user_id = ?", string(b), userID); err != nil {
					return "", err
				}
				if err := tx.Commit(); err != nil {
					return "", err
				}
				return "recovery", nil
			}
		}
	}
	return "", ErrBadCode
}
