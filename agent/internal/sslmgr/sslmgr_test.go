package sslmgr

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func pair(t *testing.T, name string, notAfter time.Time) (string, string) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	kb, _ := x509.MarshalECPrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
}

func TestCheckCustom(t *testing.T) {
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	cert, key := pair(t, "shop.test", now.Add(30*24*time.Hour))
	_, otherKey := pair(t, "shop.test", now.Add(30*24*time.Hour))
	if info, full, err := CheckCustom(customReq{Name: "shop.test", Certificate: cert, PrivateKey: key}, now); err != nil || info.DaysRemaining != 30 || !strings.Contains(string(full), "BEGIN CERTIFICATE") {
		t.Fatalf("valid pair: %v %+v", err, info)
	}
	if _, _, err := CheckCustom(customReq{Certificate: cert, PrivateKey: otherKey}, now); err == nil {
		t.Error("mismatched key accepted")
	}
	old, oldKey := pair(t, "shop.test", now.Add(-time.Hour))
	if _, _, err := CheckCustom(customReq{Certificate: old, PrivateKey: oldKey}, now); err == nil {
		t.Error("expired certificate accepted")
	}
	if _, _, err := CheckCustom(customReq{Certificate: cert + key, PrivateKey: key}, now); err == nil {
		t.Error("key in certificate field accepted")
	}
}

func TestIssueUsesWebrootAndStoresNothingItself(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("ssl.issue")
	_, err := a.Handler(context.Background(), json.RawMessage(`{"cert_name":"new.test","names":["new.test","www.new.test"],"email":"ops@example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "certbot certonly --webroot --webroot-path /var/www/whacp-acme --cert-name new.test --non-interactive --agree-tos --keep-until-expiring --expand --no-eff-email --email ops@example.com -d new.test -d www.new.test"
	if !h.Runner.Called(want) {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}
	if !h.Exists("etc/letsencrypt/renewal-hooks/deploy/whacp-reload-nginx.sh") || !h.Exists("var/www/whacp-acme") {
		t.Error("deploy hook or ACME root missing")
	}
	for _, bad := range []string{`{"cert_name":"x.test","names":["x.test"],"email":"a@b.c --staging"}`, `{"cert_name":"x.test","names":["*.x.test"]}`, `{"cert_name":"x.test","names":[]}`} {
		if _, err := a.Handler(context.Background(), json.RawMessage(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestDeleteRefusedWhileReferenced(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("ssl.delete")
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"cert_name":"example.com"}`)); err == nil || !strings.Contains(err.Error(), "still used") {
		t.Fatalf("err = %v", err)
	}
}
