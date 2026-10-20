// Package crypto provides authenticated encryption (NaCl secretbox, the same
// primitive as PHP's libsodium sodium_crypto_secretbox) for secrets the panel
// must keep: remote agent tokens, backup keys, session cookies. The key lives
// in a file readable only by the panel (default /etc/whacp/panel.key), never
// in the database. The on-the-wire format ("v1:" + base64(nonce||box)) is
// byte-compatible with app/Security/Crypto.php, so values written by the PHP
// panel decrypt here and vice versa.
package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/nacl/secretbox"
)

const (
	keyBytes   = 32 // SODIUM_CRYPTO_SECRETBOX_KEYBYTES
	nonceBytes = 24 // SODIUM_CRYPTO_SECRETBOX_NONCEBYTES
	prefix     = "v1:"
)

// Crypto seals and opens secrets with a key loaded lazily from keyFile.
type Crypto struct {
	keyFile string

	mu  sync.Mutex
	key *[keyBytes]byte
}

// New returns a Crypto that reads its key from keyFile on first use.
func New(keyFile string) *Crypto {
	return &Crypto{keyFile: keyFile}
}

// NewWithKey returns a Crypto using the supplied 32-byte key directly (for
// tests and for callers that already hold the key material).
func NewWithKey(key [keyBytes]byte) *Crypto {
	k := key
	return &Crypto{key: &k}
}

// Encrypt seals plain and returns the PHP-compatible "v1:" encoding.
func (c *Crypto) Encrypt(plain []byte) (string, error) {
	key, err := c.loadKey()
	if err != nil {
		return "", err
	}
	var nonce [nonceBytes]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	// secretbox.Seal appends the box to nonce[:], giving nonce||box exactly as
	// the PHP side stores it (libsodium prepends the Poly1305 tag to the box).
	sealed := secretbox.Seal(nonce[:], plain, &nonce, key)
	return prefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt opens a "v1:" value produced by Encrypt or by the PHP panel.
func (c *Crypto) Decrypt(encoded string) ([]byte, error) {
	key, err := c.loadKey()
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(encoded, prefix) {
		return nil, errors.New("unknown secret format")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded[len(prefix):])
	if err != nil || len(raw) <= nonceBytes {
		return nil, errors.New("corrupted secret")
	}
	var nonce [nonceBytes]byte
	copy(nonce[:], raw[:nonceBytes])
	plain, ok := secretbox.Open(nil, raw[nonceBytes:], &nonce, key)
	if !ok {
		return nil, errors.New("secret cannot be decrypted with the current key")
	}
	return plain, nil
}

// loadKey reads (once) and caches the key. The file holds the base64 of 32
// random bytes, matching the PHP panel's key file. Unlike the PHP side this
// never creates the key: the installer / PHP panel owns key creation so the
// two components can never disagree on which key is authoritative.
func (c *Crypto) loadKey() (*[keyBytes]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.key != nil {
		return c.key, nil
	}
	data, err := os.ReadFile(c.keyFile)
	if err != nil {
		return nil, fmt.Errorf("read encryption key %s: %w", c.keyFile, err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(decoded) != keyBytes {
		return nil, errors.New("the encryption key file is invalid")
	}
	var k [keyBytes]byte
	copy(k[:], decoded)
	c.key = &k
	return c.key, nil
}
