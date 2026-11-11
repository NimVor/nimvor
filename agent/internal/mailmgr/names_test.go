package mailmgr

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// fakeLE writes a certificate for the given names where certbot keeps it.
func fakeLE(t *testing.T, h *testhost.Host, cert string, names ...string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), NotAfter: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	kd, _ := x509.MarshalECPrivateKey(key)
	dir := filepath.Join(h.Base, "etc/letsencrypt/live", cert)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "fullchain.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	os.WriteFile(filepath.Join(dir, "privkey.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), 0o600)
}

func mailWithDomain(t *testing.T) (*testhost.Host, func(string, any) (map[string]any, error)) {
	t.Helper()
	h, r := setup(t)
	h.Env.Cfg.SimulateCommands = true
	if _, err := call(t, r, "mail.configure", map[string]any{"hostname": "mail.example.net", "confirm_edit_package_config": true}); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, r, "mail.domain_add", map[string]any{"domain": "abc.com"}); err != nil {
		t.Fatal(err)
	}
	return h, func(name string, p any) (map[string]any, error) { return call(t, r, name, p) }
}

func TestMailNamesAreServedBySNIWithTheirOwnCertificate(t *testing.T) {
	h, c := mailWithDomain(t)
	e := env{h.Env}
	st, err := c("mail.names_status", map[string]any{"domain": "abc.com"})
	if err != nil || st["enabled"] != false || st["mail_host"] != "mail.abc.com" || st["webmail_host"] != "webmail.abc.com" {
		t.Fatalf("status = %v %v", st, err)
	}
	if _, err := c("mail.names_status", map[string]any{"domain": "nope.com"}); code(err) != "not_found" {
		t.Errorf("unknown domain: %v", err)
	}

	// Without a certificate yet: only the challenge site exists, no SNI.
	d, _ := e.load()
	dv := d.Domains["abc.com"]
	n := namesFor("abc.com")
	dv.Names = &n
	d.Domains["abc.com"] = dv
	if err := e.applyNames(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	vh := h.Read("etc/nginx/sites-available/nimvor-mail-names.conf")
	if !strings.Contains(vh, "server_name mail.abc.com webmail.abc.com;") || !strings.Contains(vh, "acme-challenge") || strings.Contains(vh, "443") {
		t.Errorf("challenge site:\n%s", vh)
	}
	if strings.Contains(h.Read("etc/dovecot/conf.d/99-nimvor-names.conf"), "local_name") {
		t.Error("SNI before the certificate exists")
	}
	if strings.Contains(h.Read("etc/nimvor-mail/sni_maps"), "mail.abc.com") {
		t.Error("Postfix SNI before the certificate exists")
	}

	// With the certificate: Dovecot and Postfix serve it by SNI, one combined file for Postfix.
	fakeLE(t, h, "mail.abc.com", "mail.abc.com", "webmail.abc.com")
	if err := e.applyNames(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	dc := h.Read("etc/dovecot/conf.d/99-nimvor-names.conf")
	for _, want := range []string{"local_name mail.abc.com {", "ssl_cert = </etc/letsencrypt/live/mail.abc.com/fullchain.pem", "ssl_key = </etc/letsencrypt/live/mail.abc.com/privkey.pem"} {
		if !strings.Contains(dc, want) {
			t.Errorf("dovecot lacks %q:\n%s", want, dc)
		}
	}
	if !strings.Contains(h.Read("etc/nimvor-mail/sni_maps"), "mail.abc.com /etc/nimvor-mail/sni/mail.abc.com.pem") {
		t.Errorf("sni map:\n%s", h.Read("etc/nimvor-mail/sni_maps"))
	}
	pemFile := h.Read("etc/nimvor-mail/sni/mail.abc.com.pem")
	if !strings.Contains(pemFile, "EC PRIVATE KEY") || strings.Index(pemFile, "EC PRIVATE KEY") > strings.Index(pemFile, "CERTIFICATE") {
		t.Errorf("combined file must hold the key, then the chain:\n%s", pemFile)
	}
	if fi, err := os.Stat(filepath.Join(h.Base, "etc/nimvor-mail/sni/mail.abc.com.pem")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the key file must be private: %v %v", fi, err)
	}
	if !h.Runner.Called("postconf -e tls_server_sni_maps=texthash:/etc/nimvor-mail/sni_maps") {
		t.Errorf("postfix not told about the SNI map: %v", h.Runner.Calls)
	}
	// The data keeps the names; the server's own certificate is untouched.
	if d2, _ := e.load(); d2.Domains["abc.com"].Names == nil {
		t.Error("names not saved")
	}
	if !strings.Contains(h.Read("etc/dovecot/conf.d/99-nimvor.conf"), "ssl_cert = </etc/nimvor-mail/tls.crt") {
		t.Error("the server's own certificate changed")
	}

	// Turning them off removes every trace; the certificate stays.
	if _, err := c("mail.names_disable", map[string]any{"domain": "abc.com"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.Read("etc/dovecot/conf.d/99-nimvor-names.conf"), "local_name") || strings.Contains(h.Read("etc/nimvor-mail/sni_maps"), "abc.com") {
		t.Error("SNI left after turning names off")
	}
	if h.Exists("etc/nimvor-mail/sni/mail.abc.com.pem") || h.Exists("etc/nginx/sites-available/nimvor-mail-names.conf") {
		t.Error("files left after turning names off")
	}
	if !h.Runner.Called("postconf -e tls_server_sni_maps=") {
		t.Error("the SNI setting was not cleared")
	}
	if !h.Exists("etc/letsencrypt/live/mail.abc.com/fullchain.pem") {
		t.Error("the certificate was removed")
	}
}

func TestWebmailOnTheDomainsOwnNameAndRollbackOfAFailedChange(t *testing.T) {
	h, c := mailWithDomain(t)
	e := env{h.Env}
	fakeLE(t, h, "mail.abc.com", "mail.abc.com", "webmail.abc.com")
	// Webmail is on at the server's name: the domain's name gets its own HTTPS block with its own certificate.
	os.MkdirAll(filepath.Join(h.Base, "etc/nimvor-webmail"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/nimvor-webmail/webmail.json"), []byte(`{"enabled":true,"hostname":"webmail.example.net","php":"8.3"}`), 0o600)
	d, _ := e.load()
	n := namesFor("abc.com")
	dv := d.Domains["abc.com"]
	dv.Names = &n
	d.Domains["abc.com"] = dv
	if err := e.applyNames(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	vh := h.Read("etc/nginx/sites-available/nimvor-mail-names.conf")
	for _, want := range []string{"server_name mail.abc.com;", "server_name webmail.abc.com;", "ssl_certificate /etc/letsencrypt/live/mail.abc.com/fullchain.pem;", "return 301 https://", "fastcgi_pass unix:/run/php/nimvor-webmail.sock;"} {
		if !strings.Contains(vh, want) {
			t.Errorf("names site lacks %q:\n%s", want, vh)
		}
	}
	if strings.Contains(vh, "server_name mail.abc.com webmail.abc.com;") {
		t.Error("webmail.abc.com is answered twice")
	}

	// A configuration that does not validate leaves the previous files and the data alone.
	good := h.Read("etc/dovecot/conf.d/99-nimvor-names.conf")
	h.Fail["doveconf -n"] = "doveconf: Fatal: broken"
	d2, _ := e.load()
	dv2 := d2.Domains["abc.com"]
	dv2.Names = nil
	d2.Domains["abc.com"] = dv2
	if err := e.applyNames(context.Background(), d2); code(err) != "failed" {
		t.Fatalf("a broken configuration was accepted: %v", err)
	}
	delete(h.Fail, "doveconf -n")
	if h.Read("etc/dovecot/conf.d/99-nimvor-names.conf") != good || !h.Exists("etc/nginx/sites-available/nimvor-mail-names.conf") {
		t.Error("the previous files were not restored")
	}
	if d3, _ := e.load(); d3.Domains["abc.com"].Names == nil {
		t.Error("the data changed although the change failed")
	}
	_ = c
}

func TestEnablingMailNamesChecksWhatItNeedsAndIssuesTheCertificate(t *testing.T) {
	h, c := mailWithDomain(t)
	e := env{h.Env}
	h.Env.Cfg.SimulateCommands = true // no DNS lookups on the test host
	// certbot missing
	h.Runner.Unavailable["certbot"] = true
	if _, err := c("mail.names_enable", map[string]any{"domain": "abc.com"}); code(err) != "failed" || !strings.Contains(err.Error(), "certbot") {
		t.Fatalf("without certbot: %v", err)
	}
	delete(h.Runner.Unavailable, "certbot")
	if _, err := c("mail.names_enable", map[string]any{"domain": "bad domain"}); code(err) != "invalid_params" {
		t.Errorf("bad domain: %v", err)
	}
	if _, err := c("mail.names_enable", map[string]any{"domain": "other.com"}); code(err) != "not_found" {
		t.Errorf("unhosted domain: %v", err)
	}

	// The certificate is issued by certbot (webroot, both names) and everything is switched on.
	fakeLE(t, h, "mail.abc.com", "mail.abc.com", "webmail.abc.com") // what certbot leaves behind
	res, err := c("mail.names_enable", map[string]any{"domain": "abc.com", "email": "admin@abc.com"})
	if err != nil {
		t.Fatal(err)
	}
	job, _ := e.Jobs.Wait(context.Background(), res["job_id"].(string))
	if job.Status != "succeeded" {
		t.Fatalf("job = %+v", job)
	}
	if !strings.Contains(h.Read("etc/dovecot/conf.d/99-nimvor-names.conf"), "local_name mail.abc.com") {
		t.Error("the certificate was issued but Dovecot does not serve it")
	}
	calls := strings.Join(h.Runner.Calls, "\n")
	if !strings.Contains(calls, "certbot certonly --webroot") {
		t.Fatalf("certbot not asked: %v", h.Runner.Calls)
	}
	if !strings.Contains(calls, "--cert-name mail.abc.com") || !strings.Contains(calls, "-d mail.abc.com") || !strings.Contains(calls, "-d webmail.abc.com") || !strings.Contains(calls, "--email admin@abc.com") {
		t.Errorf("certbot arguments:\n%s", calls)
	}
}

// A name without a block of its own on 443 falls through to another website there (a browser forces HTTPS for
// .dev, so webmail.tplix.dev opened a different site): the names the certificate covers always answer, and
// the file follows webmail being turned on or off.
func TestNamesAlwaysAnswerOnHTTPSAndFollowWebmail(t *testing.T) {
	h, _ := mailWithDomain(t)
	e := env{h.Env}
	fakeLE(t, h, "mail.abc.com", "mail.abc.com", "webmail.abc.com")
	d, _ := e.load()
	n := namesFor("abc.com")
	dv := d.Domains["abc.com"]
	dv.Names = &n
	d.Domains["abc.com"] = dv
	if err := e.applyNames(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	file := "etc/nginx/sites-available/nimvor-mail-names.conf"
	vh := h.Read(file)
	idle := "listen 443 ssl"
	i := strings.Index(vh, idle)
	if i < 0 || !strings.Contains(vh[i:], "server_name mail.abc.com webmail.abc.com;") || !strings.Contains(vh[i:], "return 404") || strings.Contains(vh, "fastcgi_pass") {
		t.Fatalf("webmail is off: both names must answer on 443 without serving webmail:\n%s", vh)
	}

	// Webmail turned on: the same file now serves webmail on its name and keeps mail's own block.
	os.MkdirAll(filepath.Join(h.Base, "etc/nimvor-webmail"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/nimvor-webmail/webmail.json"), []byte(`{"enabled":true,"hostname":"webmail.example.net","php":"8.3"}`), 0o600)
	e.refreshNames(context.Background())
	vh = h.Read(file)
	if !strings.Contains(vh, "fastcgi_pass unix:/run/php/nimvor-webmail.sock;") || strings.Contains(vh, "server_name mail.abc.com webmail.abc.com;") || strings.Count(vh, "return 404 \"") != 1 {
		t.Fatalf("webmail on:\n%s", vh)
	}

	// And off again.
	os.WriteFile(filepath.Join(h.Base, "etc/nimvor-webmail/webmail.json"), []byte(`{"enabled":false,"hostname":"webmail.example.net","php":"8.3"}`), 0o600)
	e.refreshNames(context.Background())
	if vh = h.Read(file); strings.Contains(vh, "fastcgi_pass") || !strings.Contains(vh, "server_name mail.abc.com webmail.abc.com;") {
		t.Fatalf("webmail off again:\n%s", vh)
	}
}
