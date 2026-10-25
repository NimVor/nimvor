// Package identity ports the panel's authentication primitives. Password
// verification must byte-for-byte match PHP's password_hash output so existing
// users can log in after the migration without any re-hashing.
package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

const (
	// PasswordMinLength mirrors PasswordHasher::MIN_LENGTH.
	PasswordMinLength = 12
	// PasswordMaxLength mirrors PasswordHasher::MAX_LENGTH.
	PasswordMaxLength = 1024
)

// PasswordWeakness returns a user-facing problem with the password, or ""
// when acceptable (PasswordHasher::weakness). The username argument is
// optional (case-insensitive substring check).
func PasswordWeakness(password, username string) string {
	rs := []rune(password)
	n := len(rs)
	if n < PasswordMinLength {
		return "The password must be at least 12 characters long."
	}
	if n > PasswordMaxLength {
		return "The password is too long."
	}
	if username != "" && strings.Contains(strings.ToLower(password), strings.ToLower(username)) {
		return "The password must not contain the username."
	}
	seen := map[rune]bool{}
	for _, r := range rs {
		seen[r] = true
	}
	if len(seen) < 5 {
		return "The password is too repetitive."
	}
	return ""
}



// Argon2id parameters matching PHP 8.3's PASSWORD_ARGON2ID defaults
// (memory_cost=65536 KiB, time_cost=4, threads=1), so a hash produced here is
// indistinguishable from one produced by the PHP panel and verifies on both
// sides.
const (
	argonMemory  uint32 = 65536
	argonTime    uint32 = 4
	argonThreads uint8  = 1
	argonSaltLen        = 16
	argonKeyLen  uint32 = 32
)

// HashPassword produces an argon2id PHC string in PHP's password_hash format.
// PHP's password_verify accepts it unchanged, so a password changed through
// the Go panel still works in the PHP panel during the migration.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// NeedsRehash reports whether a stored hash should be upgraded to the current
// argon2id parameters — true for any bcrypt hash or an argon2 hash whose
// parameters differ. Mirrors PHP's password_needs_rehash against
// PASSWORD_ARGON2ID. The caller rehashes only after a successful verify.
func NeedsRehash(hash string) bool {
	if !strings.HasPrefix(hash, "$argon2id$") {
		return true
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 6 {
		return true
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return true
	}
	var m, t, p uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return true
	}
	return m != argonMemory || t != argonTime || uint8(p) != argonThreads
}

// VerifyPassword reports whether password matches the stored PHP hash. It
// supports the two formats PHP's password_hash emits in this product:
// argon2id (the default, PASSWORD_ARGON2ID) and bcrypt (PASSWORD_BCRYPT,
// including the "$2y$" prefix PHP uses).
func VerifyPassword(hash, password string) bool {
	switch {
	case strings.HasPrefix(hash, "$argon2id$"), strings.HasPrefix(hash, "$argon2i$"):
		return verifyArgon2(hash, password)
	case strings.HasPrefix(hash, "$2y$"), strings.HasPrefix(hash, "$2a$"), strings.HasPrefix(hash, "$2b$"):
		h := hash
		// Go's bcrypt understands 2a/2b; PHP writes 2y, which is wire-identical.
		if strings.HasPrefix(h, "$2y$") {
			h = "$2b$" + h[4:]
		}
		return bcrypt.CompareHashAndPassword([]byte(h), []byte(password)) == nil
	default:
		return false
	}
}

// verifyArgon2 parses a PHC argon2 string
// ($argon2id$v=19$m=65536,t=4,p=1$<salt>$<hash>) and recomputes the key with
// the embedded parameters, comparing in constant time.
func verifyArgon2(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", salt, hash]
	if len(parts) != 6 {
		return false
	}
	variant := parts[1]
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var m uint32
	var t, p uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	var got []byte
	switch variant {
	case "argon2id":
		got = argon2.IDKey([]byte(password), salt, t, m, uint8(p), uint32(len(want)))
	case "argon2i":
		got = argon2.Key([]byte(password), salt, t, m, uint8(p), uint32(len(want)))
	default:
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}
