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
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
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
	_, err := a.Handler(context.Background(), json.RawMessage(`{"cert_name":"new.test","names":["new.test","www.new.test"],"email":"ops@nimvor.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "certbot certonly --webroot --webroot-path /var/www/nimvor-acme --cert-name new.test --non-interactive --agree-tos --keep-until-expiring --expand --no-eff-email --email ops@nimvor.com -d new.test -d www.new.test"
	if !h.Runner.Called(want) {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}
	if !h.Exists("etc/letsencrypt/renewal-hooks/deploy/nimvor-reload-nginx.sh") || !h.Exists("var/www/nimvor-acme") {
		t.Error("deploy hook or ACME root missing")
	}
	for _, bad := range []string{`{"cert_name":"x.test","names":["x.test"],"email":"a@b.c --staging"}`, `{"cert_name":"x.test","names":["*.x.test"]}`, `{"cert_name":"x.test","names":[]}`} {
		if _, err := a.Handler(context.Background(), json.RawMessage(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

// A placeholder contact at a reserved domain (e.g. the installer's
// you@example.com) is dropped so issuance is not blocked by Let's
// Encrypt's "forbidden domain" rejection; certbot is asked to register
// without a contact, and the result records the ignored address.
func TestIssueDropsReservedContactEmail(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("ssl.issue")
	res, err := a.Handler(context.Background(), json.RawMessage(`{"cert_name":"shop.test","names":["shop.test"],"email":"you@example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["email_ignored"] != "you@example.com" {
		t.Errorf("email_ignored not recorded: %+v", res)
	}
	for _, c := range h.Runner.Calls {
		if strings.Contains(c, "--email you@example.com") {
			t.Errorf("forbidden contact passed to certbot: %s", c)
		}
	}
	if !h.Runner.Called("certbot certonly --webroot --webroot-path /var/www/nimvor-acme --cert-name shop.test --non-interactive --agree-tos --keep-until-expiring --expand --no-eff-email --register-unsafely-without-email -d shop.test") {
		t.Fatalf("did not register without a contact: %v", h.Runner.Calls)
	}
	for _, ok := range []string{"ops@example.company", "admin@nimvor.com", "ops@mail.realdomain.net"} {
		if reservedEmailDomain(ok) {
			t.Errorf("%s wrongly treated as reserved", ok)
		}
	}
	for _, bad := range []string{"you@example.com", "a@foo.test", "x@host.localhost", "y@dev.invalid"} {
		if !reservedEmailDomain(bad) {
			t.Errorf("%s should be treated as reserved", bad)
		}
	}
}

// A failure to reach Let's Encrypt (account registration, connection, DNS,
// clock) is reported as an outbound-connectivity problem, not as the
// inbound DNS/port-80 advice that only fits an authorization failure.
func TestACMEFailureHeadline(t *testing.T) {
	outbound := []string{
		"Unable to register an account with ACME server",
		"Error creating new account :: ...",
		"HTTPSConnectionPool: Connection refused",
		"Network is unreachable",
		"Temporary failure in name resolution",
		"The read operation timed out",
	}
	for _, o := range outbound {
		if h := acmeFailureHeadline(o); !strings.Contains(h, "reached to register the account") {
			t.Errorf("for %q got %q", o, h)
		}
	}
	if h := acmeFailureHeadline("too many certificates already issued"); !strings.Contains(h, "rate-limiting") {
		t.Errorf("rate limit not detected: %q", h)
	}
	if h := acmeFailureHeadline("challenge failed for domain shop.test"); !strings.Contains(h, "port 80 is reachable") {
		t.Errorf("authorization failure headline changed: %q", h)
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

// EPEL's certbot installs certbot-renew.timer without enabling it: the
// status says so, and turning it on enables that timer by name.
func TestRenewalTimerRHEL(t *testing.T) {
	h := testhost.New(t)
	active := map[string]bool{}
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "systemctl" && len(args) > 2 && args[0] == "is-active" {
			if active[args[2]] {
				return "active", nil, true
			}
			return "inactive", errors.New("exit status 3"), true
		}
		if id == "systemctl" && len(args) == 4 && args[0] == "enable" {
			active[args[3]] = true
		}
		return "", nil, false
	}
	os.Remove(filepath.Join(h.Base, "etc/cron.d/certbot"))
	os.MkdirAll(filepath.Join(h.Base, "usr/lib/systemd/system"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "usr/lib/systemd/system/certbot-renew.timer"), []byte("[Timer]\n"), 0o644)
	st := RenewalStatus(context.Background(), h.Env)
	if st["timer_active"] != false || st["can_enable"] != true {
		t.Fatalf("before: %v", st)
	}
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("ssl.renewal_enable")
	out, err := a.Handler(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Runner.Called("systemctl enable --now -- certbot-renew.timer") {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}
	if m := out.(map[string]any); m["timer_active"] != true || m["method"] != "certbot-renew.timer" {
		t.Fatalf("after: %v", m)
	}
}

func TestRenewalLog(t *testing.T) {
	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "var/log/letsencrypt"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "var/log/letsencrypt/letsencrypt.log.1"), []byte("2026-10-01 03:00:01,000:DEBUG:certbot._internal.main:certbot version: 2.11\n2026-10-01 03:00:02,000:INFO:certbot._internal.renewal:Certificate not yet due for renewal\n"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "var/log/letsencrypt/letsencrypt.log"), []byte("2026-10-02 03:00:02,000:DEBUG:urllib3.connectionpool:error x\n2026-10-02 03:00:03,000:INFO:certbot._internal.renewal:Renewing an existing certificate for shop.test\n"), 0o644)
	got := RenewalLog(h.Env, 10)
	if len(got) != 2 || !strings.Contains(got[0], "not yet due") || !strings.Contains(got[1], "Renewing an existing certificate for shop.test") {
		t.Fatalf("log: %q", got)
	}
}
