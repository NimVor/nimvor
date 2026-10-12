package sslmgr

import (
	"os"
	"path/filepath"
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
