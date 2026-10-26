package webauthn

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestB64U(t *testing.T) {
	b := []byte{0x00, 0x01, 0xfe, 0xff}
	s := B64U(b)
	if s != "AAH-_w" {
		t.Fatalf("B64U = %q", s)
	}
	got, err := UnB64U(s)
	if err != nil || !bytes.Equal(got, b) {
		t.Fatalf("round-trip: %v %v", got, err)
	}
}

func TestCBOREncodeDecodeCOSEKey(t *testing.T) {
	// Encode a minimal ES256 COSE key map {1:2, 3:-7, -1:1, -2:<x>, -3:<y>}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	x := k.PublicKey.X.Bytes()
	y := k.PublicKey.Y.Bytes()
	// pad to 32 bytes
	pad := func(b []byte) []byte {
		p := make([]byte, 32)
		copy(p[32-len(b):], b)
		return p
	}
	// Build CBOR manually enough for our decoder: map(5) + keys+values
	var buf bytes.Buffer
	buf.WriteByte(0xa5) // map of 5
	writeCborInt(&buf, 1)
	writeCborInt(&buf, 2)
	writeCborInt(&buf, 3)
	writeCborInt(&buf, -7)
	writeCborInt(&buf, -1)
	writeCborInt(&buf, 1)
	writeCborInt(&buf, -2)
	writeCborBytes(&buf, pad(x))
	writeCborInt(&buf, -3)
	writeCborBytes(&buf, pad(y))

	out, _, err := cborDecode(buf.Bytes(), 0, 0)
	if err != nil {
		t.Fatalf("cborDecode: %v", err)
	}
	got, ok := out.(map[any]any)
	if !ok {
		t.Fatalf("got %T", out)
	}
	if got[int64(1)] != int64(2) || got[int64(3)] != int64(-7) {
		t.Fatalf("kty/alg wrong: %v", got)
	}
	pemKey, err := CoseToKey(got)
	if err != nil {
		t.Fatalf("CoseToKey: %v", err)
	}
	if !strings.HasPrefix(pemKey, "-----BEGIN PUBLIC KEY-----") {
		t.Fatalf("expected PEM, got: %s", pemKey[:80])
	}
}

func TestEd25519COSE(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.WriteByte(0xa4)
	writeCborInt(&buf, 1)
	writeCborInt(&buf, 1)
	writeCborInt(&buf, 3)
	writeCborInt(&buf, -8)
	writeCborInt(&buf, -1)
	writeCborInt(&buf, 6)
	writeCborInt(&buf, -2)
	writeCborBytes(&buf, []byte(pub))
	out, _, err := cborDecode(buf.Bytes(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := CoseToKey(out.(map[any]any))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, "ed25519:") {
		t.Fatalf("want ed25519 prefix, got %q", stored[:20])
	}
}

func TestRSACOSE(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	n := priv.PublicKey.N.Bytes()
	e := bigEndianBytes(int64(priv.PublicKey.E))
	var buf bytes.Buffer
	buf.WriteByte(0xa4)
	writeCborInt(&buf, 1)
	writeCborInt(&buf, 3)
	writeCborInt(&buf, 3)
	writeCborInt(&buf, -257)
	writeCborInt(&buf, -1)
	writeCborBytes(&buf, n)
	writeCborInt(&buf, -2)
	writeCborBytes(&buf, e)
	out, _, err := cborDecode(buf.Bytes(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := CoseToKey(out.(map[any]any))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, "-----BEGIN PUBLIC KEY-----") {
		t.Fatalf("want PEM, got %q", stored[:40])
	}
}

func TestClientDataCheck(t *testing.T) {
	raw := []byte(`{"type":"webauthn.get","challenge":"abcd","origin":"https://panel.example","crossOrigin":false}`)
	cd, err := checkClientData(raw, "webauthn.get", "abcd", "https://panel.example")
	if err != nil || cd == nil {
		t.Fatalf("expected ok: %v", err)
	}
	if _, err := checkClientData(raw, "webauthn.create", "abcd", "https://panel.example"); err == nil {
		t.Fatal("expected wrong-type error")
	}
	if _, err := checkClientData(raw, "webauthn.get", "wrong", "https://panel.example"); err == nil {
		t.Fatal("expected bad-challenge error")
	}
	if _, err := checkClientData(raw, "webauthn.get", "abcd", "https://evil"); err == nil {
		t.Fatal("expected origin error")
	}
}

func TestParseAuthDataMinimal(t *testing.T) {
	rp := "example.com"
	rpHash := sha256.Sum256([]byte(rp))
	var buf []byte
	buf = append(buf, rpHash[:]...)
	buf = append(buf, 0x05) // flags: user present + no attested, just bit 0
	var count [4]byte
	count[3] = 3
	buf = append(buf, count[:]...)
	ad, err := ParseAuthData(buf, rp, false)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ad.SignCount != 3 {
		t.Fatalf("count=%d", ad.SignCount)
	}
}

// helpers for building CBOR in tests (just what we need: ints, bytes, map headers)
func writeCborInt(buf *bytes.Buffer, n int64) {
	if n >= 0 {
		writeCborArg(buf, 0, uint64(n))
	} else {
		writeCborArg(buf, 1, uint64(-1-n))
	}
}
func writeCborArg(buf *bytes.Buffer, major byte, v uint64) {
	switch {
	case v < 24:
		buf.WriteByte(major<<5 | byte(v))
	case v < 1<<8:
		buf.WriteByte(major<<5 | 24)
		buf.WriteByte(byte(v))
	case v < 1<<16:
		buf.WriteByte(major<<5 | 25)
		buf.WriteByte(byte(v >> 8))
		buf.WriteByte(byte(v))
	case v < 1<<32:
		buf.WriteByte(major<<5 | 26)
		for i := 24; i >= 0; i -= 8 {
			buf.WriteByte(byte(v >> i))
		}
	default:
		buf.WriteByte(major<<5 | 27)
		for i := 56; i >= 0; i -= 8 {
			buf.WriteByte(byte(v >> i))
		}
	}
}
func writeCborBytes(buf *bytes.Buffer, b []byte) {
	writeCborArg(buf, 2, uint64(len(b)))
	buf.Write(b)
}
func bigEndianBytes(n int64) []byte {
	var buf [8]byte
	for i := 7; i >= 0; i-- {
		buf[i] = byte(n)
		n >>= 8
	}
	return bytes.TrimLeft(buf[:], "\x00")
}

var _ = hex.EncodeToString // keep import
