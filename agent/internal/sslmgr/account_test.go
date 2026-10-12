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

func TestLetsEncryptAccount(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)

	// No account yet.
	res, err := call(t, r, "ssl.account", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if res["registered"] != false {
		t.Fatalf("expected no account: %+v", res)
	}

	// Registration requires accepting the Terms of Service and a real email.
	if _, err := call(t, r, "ssl.account_register", `{"email":"me@whacp.io"}`); err == nil {
		t.Error("registered without accepting the ToS")
	}
	if _, err := call(t, r, "ssl.account_register", `{"email":"you@example.com","agree_tos":true}`); err == nil {
		t.Error("accepted a reserved-domain contact")
	}
	if _, err := call(t, r, "ssl.account_register", `{"email":"not-an-email","agree_tos":true}`); err == nil {
		t.Error("accepted an invalid email")
	}

	// With the account files already on disk, a register call updates the
	// contact (update_account) rather than creating a new account.
	acctDir := filepath.Join(h.Base, "etc/letsencrypt/accounts", acmeProd, "directory", "abc123")
	os.MkdirAll(acctDir, 0o755)
	os.WriteFile(filepath.Join(acctDir, "regr.json"), []byte(`{"uri":"https://acme-v02.api.letsencrypt.org/acme/acct/42","body":{"contact":["mailto:me@whacp.io"]}}`), 0o600)

	res2, err := call(t, r, "ssl.account_register", `{"email":"me@whacp.io","agree_tos":true}`)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if res2["registered"] != true || res2["updated"] != true || res2["email"] != "me@whacp.io" {
		t.Fatalf("register result = %+v", res2)
	}
	if !h.Runner.Called("certbot update_account --non-interactive -m me@whacp.io") {
		t.Fatalf("did not update the account: %v", h.Runner.Calls)
	}

	// ssl.account now reports the stored account.
	s, _ := call(t, r, "ssl.account", `{}`)
	if s["registered"] != true || s["email"] != "me@whacp.io" || s["account_uri"] != "https://acme-v02.api.letsencrypt.org/acme/acct/42" {
		t.Fatalf("status = %+v", s)
	}
}

// A fresh registration (no account on disk) runs `certbot register`.
func TestLetsEncryptAccountFreshRegister(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	if _, err := call(t, r, "ssl.account_register", `{"email":"ops@whacp.io","agree_tos":true}`); err != nil {
		t.Fatal(err)
	}
	if !h.Runner.Called("certbot register --non-interactive --agree-tos -m ops@whacp.io") {
		t.Fatalf("did not register a new account: %v", h.Runner.Calls)
	}
}

// ZeroSSL needs External Account Binding credentials and is registered
// against its own ACME directory (--server).
func TestZeroSSLAccountNeedsEAB(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)

	// The provider is reported as needing EAB.
	st, _ := call(t, r, "ssl.account", `{"provider":"zerossl"}`)
	if st["requires_eab"] != true || st["provider"] != "zerossl" {
		t.Fatalf("zerossl status = %+v", st)
	}

	// Registration without EAB is refused.
	if _, err := call(t, r, "ssl.account_register", `{"provider":"zerossl","email":"ops@whacp.io","agree_tos":true}`); err == nil {
		t.Error("registered ZeroSSL without EAB")
	}
	// With EAB it registers against ZeroSSL's directory.
	if _, err := call(t, r, "ssl.account_register", `{"provider":"zerossl","email":"ops@whacp.io","agree_tos":true,"eab_kid":"KID1","eab_hmac_key":"HMAC1"}`); err != nil {
		t.Fatal(err)
	}
	if !h.Runner.Called("certbot register --non-interactive --agree-tos -m ops@whacp.io --eab-kid KID1 --eab-hmac-key HMAC1 --server https://acme.zerossl.com/v2/DV90") {
		t.Fatalf("ZeroSSL register args wrong: %v", h.Runner.Calls)
	}
}

// Buypass has no EAB and registers against its own directory.
func TestBuypassAccountRegister(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	if _, err := call(t, r, "ssl.account_register", `{"provider":"buypass","email":"ops@whacp.io","agree_tos":true}`); err != nil {
		t.Fatal(err)
	}
	if !h.Runner.Called("certbot register --non-interactive --agree-tos -m ops@whacp.io --server https://api.buypass.com/acme/directory") {
		t.Fatalf("Buypass register args wrong: %v", h.Runner.Calls)
	}
	// An unknown provider is refused.
	if _, err := call(t, r, "ssl.account_register", `{"provider":"nope","email":"ops@whacp.io","agree_tos":true}`); err == nil {
		t.Error("accepted an unknown provider")
	}
}

// Issuance against a non-default CA adds --server for that provider.
func TestIssueAgainstAlternateProvider(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("ssl.issue")
	if _, err := a.Handler(context.Background(), []byte(`{"cert_name":"shop.test","names":["shop.test"],"email":"ops@whacp.io","provider":"buypass"}`)); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range h.Runner.Calls {
		if strings.Contains(c, "--server https://api.buypass.com/acme/directory") && strings.Contains(c, "certonly") {
			found = true
		}
	}
	if !found {
		t.Fatalf("issuance did not target Buypass: %v", h.Runner.Calls)
	}
	if _, err := a.Handler(context.Background(), []byte(`{"cert_name":"x.test","names":["x.test"],"provider":"nope"}`)); err == nil {
		t.Error("accepted an unknown provider at issuance")
	}
}
