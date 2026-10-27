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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
	"github.com/NimVor/nimvor/agent/internal/tlsprofile"
)

func call(t *testing.T, r *actions.Registry, name, params string) (map[string]any, error) {
	t.Helper()
	a, err := r.Lookup(name)
	if err != nil {
		t.Fatalf("no %s", name)
	}
	res, err := a.Handler(context.Background(), json.RawMessage(params))
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(res)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m, nil
}

func TestCSRKeyStaysOnServerAndInstallsAgainstCASignedCert(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	m, err := call(t, r, "ssl.csr_create", `{"names":["shop.test","www.shop.test"],"organization":"Shop Ltd","country":"DE","key_type":"rsa2048"}`)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(m)
	if strings.Contains(string(b), "PRIVATE KEY") {
		t.Fatal("the private key was returned")
	}
	id := m["id"].(string)
	block, _ := pem.Decode([]byte(m["csr"].(string)))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil || csr.Subject.CommonName != "shop.test" || len(csr.DNSNames) != 2 || csr.Subject.Organization[0] != "Shop Ltd" {
		t.Fatalf("csr: %v %+v", err, csr)
	}
	keyFile := filepath.Join(h.Base, h.Env.Cfg.StateDir, "keys", id, "privkey.pem")
	if st, err := os.Stat(keyFile); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", err, st)
	}
	// A "CA" signs the CSR; the certificate is installed with the stored key.
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := h.Env.Now()
	caTpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(1000 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	leafTpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: csr.Subject, DNSNames: csr.DNSNames, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(900 * time.Hour)}
	leafDER, _ := x509.CreateCertificate(rand.Reader, leafTpl, ca, csr.PublicKey, caKey)
	leafPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}))
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	js := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	// Wrong domain, wrong chain order and a key plus key_id are refused.
	for _, bad := range []string{
		`{"name":"shop.test","key_id":"` + id + `","certificate":` + js(leafPEM) + `,"domain":"other.test"}`,
		`{"name":"shop.test","key_id":"` + id + `","certificate":` + js(leafPEM) + `,"chain":` + js(leafPEM) + `}`,
		`{"name":"shop.test","key_id":"` + id + `","certificate":` + js(leafPEM) + `,"private_key":"x"}`,
		`{"name":"shop.test","key_id":"../../etc","certificate":` + js(leafPEM) + `}`,
	} {
		if _, err := call(t, r, "ssl.custom", bad); err == nil {
			t.Errorf("accepted: %.120s", bad)
		}
	}
	res, err := call(t, r, "ssl.custom", `{"name":"shop.test","key_id":"`+id+`","certificate":`+js(leafPEM)+`,"chain":`+js(caPEM)+`,"domain":"www.shop.test"}`)
	if err != nil {
		t.Fatal(err)
	}
	if c := res["certificate"].(map[string]any); c["chain_length"].(float64) != 2 {
		t.Fatalf("installed: %+v", res)
	}
	// The store lists it; the key list does not contain keys.
	st, _ := call(t, r, "ssl.store", `{}`)
	found := false
	for _, c := range st["certificates"].([]any) {
		if cm := c.(map[string]any); cm["source"] == "nimvor" && cm["name"] == "shop.test" {
			found = cm["deletable"] == true
		}
	}
	if !found {
		t.Fatalf("store: %+v", st["certificates"])
	}
	keys, _ := call(t, r, "ssl.keys", `{}`)
	if kb, _ := json.Marshal(keys); strings.Contains(string(kb), "PRIVATE KEY") || len(keys["keys"].([]any)) != 1 {
		t.Fatalf("keys: %s", kb)
	}
	if _, err := call(t, r, "ssl.custom_delete", `{"name":"shop.test"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, r, "ssl.key_delete", `{"id":"`+id+`"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Fatal("key file left behind")
	}
	// No copy of the deleted key in configuration backups.
	filepath.Walk(filepath.Join(h.Base, h.Env.Cfg.StateDir), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "PRIVATE KEY") {
				t.Errorf("private key left in %s", p)
			}
		}
		return nil
	})
}

func TestSelfSignedInstall(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	m, err := call(t, r, "ssl.csr_create", `{"names":["intranet.test"],"self_signed_days":365}`)
	if err != nil || m["self_signed"] != true {
		t.Fatalf("%v %+v", err, m)
	}
	res, err := call(t, r, "ssl.custom", `{"name":"intranet.test","key_id":"`+m["id"].(string)+`","use_self_signed":true,"domain":"intranet.test"}`)
	if err != nil || res["certificate"].(map[string]any)["self_signed"] != true {
		t.Fatalf("%v %+v", err, res)
	}
	for _, bad := range []string{`{"names":["a b"]}`, `{"names":["x.test"],"country":"Germany"}`, `{"names":["x.test"],"key_type":"dsa"}`, `{"names":["x.test"],"self_signed_days":9999}`} {
		if _, err := call(t, r, "ssl.csr_create", bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestTLSProfileMigratesVhostsAndRollsBack(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	vh := filepath.Join(h.Base, h.Env.M.NginxAvailable, "nimvor-shop.test.conf")
	os.MkdirAll(filepath.Dir(vh), 0o755)
	os.WriteFile(vh, []byte("server {\n    listen 443 ssl;\n"+tlsprofile.LegacyBlock+"}\n"), 0o644)
	g, _ := call(t, r, "ssl.tls_get", `{}`)
	if g["legacy_vhosts"].(float64) != 1 {
		t.Fatalf("get: %+v", g)
	}
	if _, err := call(t, r, "ssl.tls_set", `{"protocols":"tls13","ciphers":"intermediate","session_tickets":false}`); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(vh)
	if !strings.Contains(string(b), "include "+tlsprofile.Path(h.Env)+";") || strings.Contains(string(b), "ssl_protocols") {
		t.Fatalf("vhost:\n%s", b)
	}
	snip, _ := os.ReadFile(filepath.Join(h.Base, tlsprofile.Path(h.Env)))
	if !strings.Contains(string(snip), "ssl_protocols TLSv1.3;") || !strings.Contains(string(snip), "ssl_session_tickets off;") {
		t.Fatalf("snippet:\n%s", snip)
	}
	// nginx -t fails: everything goes back.
	h.Fail["nginx -t"] = "emerg"
	if _, err := call(t, r, "ssl.tls_set", `{"protocols":"tls12","ciphers":"default","session_tickets":true}`); err == nil {
		t.Fatal("accepted despite nginx -t")
	}
	if s2, _ := os.ReadFile(filepath.Join(h.Base, tlsprofile.Path(h.Env))); string(s2) != string(snip) {
		t.Fatal("snippet not restored")
	}
	if _, err := call(t, r, "ssl.tls_set", `{"protocols":"ssl3","ciphers":"default"}`); err == nil {
		t.Fatal("bad profile accepted")
	}
}
