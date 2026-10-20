// Package identity ports the panel's authentication primitives. Password
// verification must byte-for-byte match PHP's password_hash output so existing
// users can log in after the migration without any re-hashing.
package identity

import (
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

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
