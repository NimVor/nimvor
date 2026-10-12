package tlsaudit

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"
)

// caAndLeaf makes a CA and a leaf certificate signed by it with the given
// signature algorithm, for "example.test".
func caAndLeaf(t *testing.T, leafSig x509.SignatureAlgorithm, notBefore, notAfter time.Time) tls.Certificate {
	t.Helper()
	caKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)

	leafKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "example.test"},
		DNSNames: []string{"example.test"}, NotBefore: notBefore, NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		SignatureAlgorithm: leafSig,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("leaf: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}
}

func serve(t *testing.T, cfg *tls.Config) Endpoint {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				tc := c.(*tls.Conn)
				tc.Handshake()
				tc.Close()
			}(c)
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	return Endpoint{Host: "127.0.0.1", Port: port, ServerName: "example.test", Kind: "website"}
}

func has(r Result, issue string) bool {
	for _, f := range r.Findings {
		if f.Issue == issue {
			return true
		}
	}
	return false
}

// A weak server: TLS 1.0 accepted, a CBC-SHA cipher offered, and a SHA-1
// leaf signed by a (test) CA.
func TestProbeWeakServer(t *testing.T) {
	cert := caAndLeaf(t, x509.SHA1WithRSA, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS10,
		MaxVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_128_CBC_SHA, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256},
	}
	ep := serve(t, cfg)
	r := Probe(context.Background(), ep)
	if !r.Reachable {
		t.Fatal("not reachable")
	}
	for _, want := range []string{"protocol-weak", "cipher-weak", "cert-sha1", "chain"} {
		if !has(r, want) {
			t.Errorf("missing finding %q; got %+v", want, r.Findings)
		}
	}
}

// A modern server: TLS 1.2+ only, strong ciphers, a SHA-256 leaf. The chain
// is still untrusted (a test CA), so a chain finding is expected, but none
// of the weak-protocol, weak-cipher or SHA-1 findings must appear.
func TestProbeModernServer(t *testing.T) {
	cert := caAndLeaf(t, x509.SHA256WithRSA, time.Now().Add(-time.Hour), time.Now().Add(24*time.Hour))
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	ep := serve(t, cfg)
	r := Probe(context.Background(), ep)
	if !r.Reachable {
		t.Fatal("not reachable")
	}
	for _, bad := range []string{"protocol-weak", "cipher-weak", "cert-sha1"} {
		if has(r, bad) {
			t.Errorf("unexpected finding %q; got %+v", bad, r.Findings)
		}
	}
}

// An expired leaf is reported.
func TestProbeExpiredCert(t *testing.T) {
	cert := caAndLeaf(t, x509.SHA256WithRSA, time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour))
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	ep := serve(t, cfg)
	r := Probe(context.Background(), ep)
	if !has(r, "cert-expired") {
		t.Errorf("expired cert not detected: %+v", r.Findings)
	}
}

// An unreachable port is reported as such, not as a crash.
func TestProbeUnreachable(t *testing.T) {
	r := Probe(context.Background(), Endpoint{Host: "127.0.0.1", Port: 1, Kind: "website"})
	if r.Reachable {
		t.Error("port 1 should not be reachable")
	}
}

func TestClassifyVerifyError(t *testing.T) {
	if classifyVerifyError(x509.HostnameError{}) != "hostname" {
		t.Error("hostname")
	}
	if classifyVerifyError(x509.UnknownAuthorityError{}) != "untrusted" {
		t.Error("untrusted")
	}
	if classifyVerifyError(x509.CertificateInvalidError{Reason: x509.Expired}) != "expired" {
		t.Error("expired")
	}
}
