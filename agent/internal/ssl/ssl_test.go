package ssl

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/filesystem"
)

func writeCert(t *testing.T, dir string, names []string, notAfter time.Time) string {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    notAfter.Add(-90 * 24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "cert.pem")
	os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	return "/cert.pem"
}

func TestInspect(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	cert := writeCert(t, dir, []string{"example.com", "*.example.com"}, now.Add(10*24*time.Hour))
	os.WriteFile(filepath.Join(dir, "key.pem"), []byte("not read"), 0o644)
	root, _ := filesystem.NewRoot(dir)

	c := Inspect(root, cert, "/key.pem", now)
	if c.Error != "" {
		t.Fatal(c.Error)
	}
	if c.DaysRemaining != 10 || c.Status() != "expiring" || c.Expired {
		t.Errorf("days=%d status=%s", c.DaysRemaining, c.Status())
	}
	if !c.SelfSigned || !c.KeyExists || !c.KeyWorldReadable {
		t.Errorf("flags: %+v", c)
	}
	for host, want := range map[string]bool{"example.com": true, "www.example.com": true, "a.b.example.com": false, "example.org": false} {
		if c.Covers(host) != want {
			t.Errorf("Covers(%q) != %v", host, want)
		}
	}
	later := Inspect(root, cert, "", now.Add(30*24*time.Hour))
	if !later.Expired || later.Status() != "expired" {
		t.Error("expected expired certificate")
	}
}

func TestInspectMissingAndMalformed(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bad.pem"), []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"), 0o644)
	root, _ := filesystem.NewRoot(dir)
	if c := Inspect(root, "/missing.pem", "", time.Now()); c.Status() != "error" {
		t.Error("missing cert should be an error")
	}
	if c := Inspect(root, "/bad.pem", "", time.Now()); c.Status() != "error" {
		t.Error("malformed cert should be an error")
	}
}

func TestDiscoverLetsEncryptFixture(t *testing.T) {
	abs, _ := filepath.Abs("../../tests/fixtures/ubuntu-host")
	root, _ := filesystem.NewRoot(abs)
	certs := DiscoverLetsEncrypt(root, "/etc/letsencrypt", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if len(certs) != 1 || certs[0].Name != "example.com" {
		t.Fatalf("certs = %+v", certs)
	}
	c := certs[0]
	if c.Authenticator != "webroot" || c.Webroot != "/var/www/example.com/public" {
		t.Errorf("renewal = %+v", c)
	}
	if !c.Certificate.Covers("www.example.com") || c.Certificate.KeyExists {
		t.Errorf("certificate = %+v", c.Certificate)
	}
}
