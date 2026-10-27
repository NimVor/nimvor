package sslmgr

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Let's Encrypt ACME account hosts certbot stores accounts under.
const (
	acmeProd    = "acme-v02.api.letsencrypt.org"
	acmeStaging = "acme-staging-v02.api.letsencrypt.org"
	leTermsURL  = "https://letsencrypt.org/repository/"
)

// Provider is an ACME certificate authority NimVor can issue from. Directory
// is the ACME directory URL passed to certbot's --server; it is empty for
// Let's Encrypt, which is certbot's built-in default. AccountPath is where
// certbot keeps the account on disk (the server's netloc plus the directory
// URL's path), used to read the account state. RequiresEAB marks providers
// that need External Account Binding credentials (an EAB key id and HMAC
// key) to register, obtained from the provider's dashboard.
type Provider struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Directory   string `json:"directory"`
	AccountPath string `json:"-"`
	RequiresEAB bool   `json:"requires_eab"`
	TermsURL    string `json:"terms_url"`
}

// Providers is the fixed set of certificate authorities. The directory URLs
// are constants here (never taken from the request), so certbot's --server
// only ever receives a vetted address.
var Providers = []Provider{
	{ID: "letsencrypt", Label: "Let's Encrypt", Directory: "", AccountPath: acmeProd + "/directory", RequiresEAB: false, TermsURL: leTermsURL},
	{ID: "zerossl", Label: "ZeroSSL", Directory: "https://acme.zerossl.com/v2/DV90", AccountPath: "acme.zerossl.com/v2/DV90", RequiresEAB: true, TermsURL: "https://zerossl.com/terms/"},
	{ID: "buypass", Label: "Buypass Go", Directory: "https://api.buypass.com/acme/directory", AccountPath: "api.buypass.com/acme/directory", RequiresEAB: false, TermsURL: "https://www.buypass.com/ssl/products/acme"},
}

// provider resolves an id to a provider; an empty id is Let's Encrypt.
func provider(id string) (Provider, bool) {
	if id == "" {
		id = "letsencrypt"
	}
	for _, p := range Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// accountPathFor is where certbot stores the account for this provider and
// environment (Let's Encrypt staging lives under its own host).
func accountPathFor(p Provider, staging bool) string {
	if p.ID == "letsencrypt" && staging {
		return acmeStaging + "/directory"
	}
	return p.AccountPath
}

// accountStatus reports whether certbot holds an account at accountPath, and
// best-effort its account URL and contact email (older certbot leaves the
// contact out of regr.json, so email may be "").
func accountStatus(env *host.Env, accountPath string) (registered bool, uri, email string) {
	base := filepath.Join("/etc/letsencrypt/accounts", accountPath)
	dirs, err := env.Root.ReadDir(base)
	if err != nil {
		return false, "", ""
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		b, err := env.Root.ReadFile(filepath.Join(base, d.Name(), "regr.json"))
		if err != nil {
			continue
		}
		var r struct {
			URI  string `json:"uri"`
			Body struct {
				Contact []string `json:"contact"`
			} `json:"body"`
		}
		if json.Unmarshal(b, &r) == nil {
			registered = true
			uri = r.URI
			for _, c := range r.Body.Contact {
				email = strings.TrimPrefix(c, "mailto:")
			}
			return
		}
	}
	return
}

func registerAccount(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "ssl.account", Description: "The ACME account certbot holds for a certificate authority (Let's Encrypt, ZeroSSL or Buypass): whether one is registered, its contact email and account URL, whether the provider needs External Account Binding, and the Terms of Service link. The provider parameter selects which (default: letsencrypt).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Provider string `json:"provider"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		p, ok := provider(in.Provider)
		if !ok {
			return nil, actions.InvalidParams("provider: unknown certificate authority")
		}
		reg, uri, email := accountStatus(env, p.AccountPath)
		list := make([]Provider, len(Providers))
		copy(list, Providers)
		out := map[string]any{
			"providers":         list,
			"provider":          p.ID,
			"label":             p.Label,
			"certbot_installed": env.Runner.Available("certbot"),
			"registered":        reg,
			"email":             email,
			"account_uri":       uri,
			"requires_eab":      p.RequiresEAB,
			"terms_url":         p.TermsURL,
		}
		if p.ID == "letsencrypt" {
			stageReg, _, _ := accountStatus(env, accountPathFor(p, true))
			out["staging_registered"] = stageReg
		}
		return out, nil
	}})

	r.Register(actions.Action{Name: "ssl.account_register", Mutating: true, Timeout: 2 * time.Minute, Description: "Register the ACME account for a provider (Let's Encrypt, ZeroSSL or Buypass), or update its contact email, after accepting the Terms of Service (agree_tos). ZeroSSL needs External Account Binding credentials (eab_kid and eab_hmac_key from your ZeroSSL account). A reserved/example address is refused.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Provider   string `json:"provider"`
			Email      string `json:"email"`
			AgreeTOS   bool   `json:"agree_tos"`
			Staging    bool   `json:"staging"`
			EABKID     string `json:"eab_kid"`
			EABHMACKey string `json:"eab_hmac_key"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		p, ok := provider(in.Provider)
		if !ok {
			return nil, actions.InvalidParams("provider: unknown certificate authority")
		}
		if !in.AgreeTOS {
			return nil, actions.InvalidParams("agree_tos: you must accept the Terms of Service to register")
		}
		in.Email = strings.ToLower(strings.TrimSpace(in.Email))
		if in.Email == "" || !emailRe.MatchString(in.Email) {
			return nil, actions.InvalidParams("email: enter a valid contact address for renewal notices")
		}
		if reservedEmailDomain(in.Email) {
			return nil, actions.InvalidParams("email: a reserved/example domain (e.g. example.com) is not accepted; use a real address")
		}
		in.EABKID = strings.TrimSpace(in.EABKID)
		in.EABHMACKey = strings.TrimSpace(in.EABHMACKey)
		if p.RequiresEAB && (in.EABKID == "" || in.EABHMACKey == "") {
			return nil, actions.InvalidParams("eab_kid, eab_hmac_key: " + p.Label + " needs External Account Binding credentials; copy them from your " + p.Label + " account")
		}
		if !env.Runner.Available("certbot") {
			return nil, actions.Failed("certbot is not installed. Install it on the Packages page (package: certbot).", "")
		}
		acctPath := accountPathFor(p, in.Staging)
		registered, _, _ := accountStatus(env, acctPath)
		var args []string
		if registered {
			// update_account only changes the contact; EAB is a one-time
			// binding from registration, so it is not resent here.
			args = []string{"update_account", "--non-interactive", "-m", in.Email}
		} else {
			args = []string{"register", "--non-interactive", "--agree-tos", "-m", in.Email}
			if p.RequiresEAB {
				args = append(args, "--eab-kid", in.EABKID, "--eab-hmac-key", in.EABHMACKey)
			}
		}
		if p.Directory != "" {
			args = append(args, "--server", p.Directory)
		} else if in.Staging {
			args = append(args, "--staging")
		}
		out, err := env.Runner.RunWith(ctx, "certbot", execx.Opts{Timeout: 90 * time.Second}, args...)
		if err != nil {
			return nil, actions.Failed(acmeFailureHeadline(string(out)), host.Output(out))
		}
		reg, uri, email := accountStatus(env, acctPath)
		return map[string]any{"provider": p.ID, "registered": reg, "updated": registered, "email": email, "account_uri": uri}, nil
	}})
}
