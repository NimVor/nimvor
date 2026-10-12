package sslmgr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

// After the server host name changes, the challenge site must be rewritten
// to serve the new name — otherwise Nginx no longer matches it and the ACME
// webroot check 404s. ensureHostnameSite used to skip the rewrite whenever
// the file already existed.
func TestHostnameSiteRewrittenAfterRename(t *testing.T) {
	h := testhost.New(t)
	site := filepath.Join(h.Base, "etc/nginx/sites-available/whacp-hostname.conf")
	os.WriteFile(site, []byte("server {\n    listen 80;\n    server_name vmi3612939.contaboserver.net;\n}\n"), 0o644)

	if err := ensureHostnameSite(context.Background(), h.Env, "vc100.oursvrs.com"); err != nil {
		t.Fatalf("ensureHostnameSite: %v", err)
	}
	got := h.Read("etc/nginx/sites-available/whacp-hostname.conf")
	if !strings.Contains(got, "server_name vc100.oursvrs.com;") {
		t.Errorf("challenge site not updated to the new host name:\n%s", got)
	}
	if strings.Contains(got, "contaboserver.net") {
		t.Errorf("old host name left in the challenge site:\n%s", got)
	}
	if !strings.Contains(got, "/.well-known/acme-challenge/") {
		t.Errorf("challenge location missing:\n%s", got)
	}
	// Re-running with the same name is a no-op (idempotent).
	if err := ensureHostnameSite(context.Background(), h.Env, "vc100.oursvrs.com"); err != nil {
		t.Fatal(err)
	}
}

const panelConf = `# Managed by WhaCP (install.sh --public-https): the panel over HTTPS.
server {
    listen 8443 ssl;
    server_name _;
    ssl_certificate     /etc/ssl/certs/whacp-panel.crt;
    ssl_certificate_key /etc/ssl/private/whacp-panel.key;
    ssl_protocols TLSv1.2 TLSv1.3;
    location / {
        proxy_pass http://127.0.0.1:8090;
    }
}
`

func TestPanelCertificate(t *testing.T) {
	h := testhost.New(t)
	os.WriteFile(filepath.Join(h.Base, "etc/nginx/sites-available/whacp-panel.conf"), []byte(panelConf), 0o644)
	os.WriteFile(filepath.Join(h.Base, "etc/letsencrypt/live/example.com/privkey.pem"), []byte("key"), 0o600)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	if _, err := call(t, r, "ssl.service_cert", `{"service":"panel","cert_name":"nope.test"}`); err == nil {
		t.Fatal("unknown certificate accepted")
	}
	if _, err := call(t, r, "ssl.service_cert", `{"service":"panel","cert_name":"example.com"}`); err != nil {
		t.Fatal(err)
	}
	got := h.Read("etc/nginx/sites-available/whacp-panel.conf")
	for _, want := range []string{"ssl_certificate     /etc/letsencrypt/live/example.com/fullchain.pem;", "ssl_certificate_key /etc/letsencrypt/live/example.com/privkey.pem;", "include /etc/nginx/whacp-tls.conf;", "proxy_pass http://127.0.0.1:8090;"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q missing:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ssl_protocols") || !h.Exists("etc/nginx/whacp-tls.conf") {
		t.Fatalf("TLS lines not moved to the snippet:\n%s", got)
	}
	// Back to the installer's certificate.
	if _, err := call(t, r, "ssl.service_cert", `{"service":"panel","cert_name":""}`); err != nil {
		t.Fatal(err)
	}
	if got := h.Read("etc/nginx/sites-available/whacp-panel.conf"); !strings.Contains(got, "ssl_certificate     /etc/ssl/certs/whacp-panel.crt;") {
		t.Fatalf("not restored:\n%s", got)
	}
	if _, err := call(t, r, "ssl.service_cert", `{"service":"ftp","cert_name":"example.com"}`); err == nil {
		t.Fatal("unknown service accepted")
	}
}

func TestMailTLS(t *testing.T) {
	h := testhost.New(t)
	drop := "# Managed by WhaCP (email).\nssl = required\nssl_cert = </etc/whacp-mail/tls.crt\nssl_key = </etc/whacp-mail/tls.key\nssl_min_protocol = TLSv1.2\n\npassdb {\n}\n"
	os.MkdirAll(filepath.Join(h.Base, "etc/dovecot/conf.d"), 0o755)
	os.WriteFile(filepath.Join(h.Base, h.Env.M.DovecotDropIn), []byte(drop), 0o644)
	os.MkdirAll(filepath.Join(h.Base, "etc/postfix"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/postfix/main.cf"), []byte("myhostname = mail.test\n"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "etc/letsencrypt/live/example.com/privkey.pem"), []byte("key"), 0o600)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	if _, err := call(t, r, "ssl.mail_tls", `{"protocols":"tls12","ciphers":"intermediate"}`); err != nil {
		t.Fatal(err)
	}
	got := h.Read(h.Env.M.DovecotDropIn)
	if !strings.Contains(got, "ssl_min_protocol = TLSv1.2\nssl_cipher_list = ECDHE-") || !strings.Contains(got, "passdb {") {
		t.Fatalf("drop-in:\n%s", got)
	}
	for _, c := range []string{"postconf -e smtpd_tls_mandatory_ciphers=high", "postconf -e smtpd_tls_mandatory_protocols=!SSLv2, !SSLv3, !TLSv1, !TLSv1.1", "postfix check", "doveconf -n", "systemctl reload -- dovecot.service"} {
		if !h.Runner.Called(c) {
			t.Fatalf("%s missing: %v", c, h.Runner.Calls)
		}
	}
	if _, err := call(t, r, "ssl.mail_tls", `{"protocols":"tls13","ciphers":"intermediate"}`); err != nil {
		t.Fatal(err)
	}
	if got := h.Read(h.Env.M.DovecotDropIn); !strings.Contains(got, "ssl_min_protocol = TLSv1.3\n") || strings.Contains(got, "ssl_cipher_list") {
		t.Fatalf("tls13 drop-in:\n%s", got)
	}
	// The mail certificate.
	if _, err := call(t, r, "ssl.service_cert", `{"service":"mail","cert_name":"example.com"}`); err != nil {
		t.Fatal(err)
	}
	if got := h.Read(h.Env.M.DovecotDropIn); !strings.Contains(got, "ssl_cert = </etc/letsencrypt/live/example.com/fullchain.pem") {
		t.Fatalf("cert drop-in:\n%s", got)
	}
	if !h.Runner.Called("postconf -e smtpd_tls_cert_file=/etc/letsencrypt/live/example.com/fullchain.pem") {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}
	// A failed check restores the files.
	before := h.Read(h.Env.M.DovecotDropIn)
	h.Fail["doveconf"] = "doveconf: Fatal: bad"
	if _, err := call(t, r, "ssl.mail_tls", `{"protocols":"tls12","ciphers":"default"}`); err == nil {
		t.Fatal("failed check accepted")
	}
	if h.Read(h.Env.M.DovecotDropIn) != before {
		t.Fatal("drop-in not restored")
	}
}
