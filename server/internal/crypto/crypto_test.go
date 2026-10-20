package crypto

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// These fixtures were produced by the PHP panel's Security\Crypto path
// (sodium_crypto_secretbox) to prove byte-level cross-compatibility: a secret
// sealed by PHP must open here unchanged.
const (
	phpKeyB64 = "zacxMfU/LcoAj8KKhaKbjGpItIKf8IFiz1CfR+ThT0c="
	phpPlain  = `agent-token-secret "value" 123`
	phpEnc    = "v1:D3Eb2djrzpc0MfDqqDrpDIc6YVWkyD1lBk3jN3PJW/BM1R0t0stTou75DAtLHqzO5Avqh1M3uVUBi308+E/3eFzLBAaX1Q=="
)

func keyFixture(t *testing.T) *Crypto {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(phpKeyB64)
	if err != nil {
		t.Fatal(err)
	}
	var k [keyBytes]byte
	copy(k[:], raw)
	return NewWithKey(k)
}

func TestDecryptPHPValue(t *testing.T) {
	got, err := keyFixture(t).Decrypt(phpEnc)
	if err != nil {
		t.Fatalf("decrypt PHP value: %v", err)
	}
	if string(got) != phpPlain {
		t.Fatalf("got %q, want %q", got, phpPlain)
	}
}

func TestRoundTrip(t *testing.T) {
	c := keyFixture(t)
	for _, plain := range []string{"", "x", phpPlain, string(bytes.Repeat([]byte{0}, 1000))} {
		enc, err := c.Encrypt([]byte(plain))
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		got, err := c.Decrypt(enc)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if string(got) != plain {
			t.Fatalf("round trip mismatch for %q", plain)
		}
	}
}

func TestDecryptRejectsTampering(t *testing.T) {
	c := keyFixture(t)
	enc, _ := c.Encrypt([]byte("secret"))
	// Flip a byte in the base64 body.
	b := []byte(enc)
	b[len(b)-3] ^= 0x01
	if _, err := c.Decrypt(string(b)); err == nil {
		t.Fatal("tampered ciphertext decrypted without error")
	}
	if _, err := c.Decrypt("garbage"); err == nil {
		t.Fatal("non-v1 value decrypted without error")
	}
}

func TestLoadKeyFromFile(t *testing.T) {
	dir := t.TempDir()
	kf := filepath.Join(dir, "panel.key")
	if err := os.WriteFile(kf, []byte(phpKeyB64+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A file-backed Crypto must open a PHP value too (whitespace trimmed).
	got, err := New(kf).Decrypt(phpEnc)
	if err != nil {
		t.Fatalf("decrypt with file key: %v", err)
	}
	if string(got) != phpPlain {
		t.Fatalf("got %q", got)
	}
	if _, err := New(filepath.Join(dir, "missing")).Decrypt(phpEnc); err == nil {
		t.Fatal("missing key file did not error")
	}
}
