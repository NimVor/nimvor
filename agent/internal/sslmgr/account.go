package sslmgr

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// The Let's Encrypt ACME endpoints certbot stores accounts under.
const (
	acmeProd    = "acme-v02.api.letsencrypt.org"
	acmeStaging = "acme-staging-v02.api.letsencrypt.org"
	// The subscriber agreement landing page (the exact versioned PDF is
	// whatever certbot --agree-tos accepts at registration time).
	leTermsURL = "https://letsencrypt.org/repository/"
)

// accountStatus reports whether certbot holds a Let's Encrypt account for
// the given ACME server, and best-effort its account URL and contact email
// (older certbot leaves the contact out of regr.json, so email may be "").
func accountStatus(env *host.Env, server string) (registered bool, uri, email string) {
	base := filepath.Join("/etc/letsencrypt/accounts", server, "directory")
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
	r.Register(actions.Action{Name: "ssl.account", Description: "The Let's Encrypt account certbot holds (whether one is registered, its contact email and account URL) for the production and staging endpoints, and the Terms of Service link.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		prodReg, prodURI, prodEmail := accountStatus(env, acmeProd)
		stageReg, _, _ := accountStatus(env, acmeStaging)
		return map[string]any{
			"provider":           "Let's Encrypt",
			"certbot_installed":  env.Runner.Available("certbot"),
			"registered":         prodReg,
			"email":              prodEmail,
			"account_uri":        prodURI,
			"staging_registered": stageReg,
			"terms_url":          leTermsURL,
		}, nil
	}})

	r.Register(actions.Action{Name: "ssl.account_register", Mutating: true, Timeout: 2 * time.Minute, Description: "Register the Let's Encrypt account, or update its contact email, after accepting the Terms of Service (agree_tos). Use it to create the account up front, or to recreate it when certbot has no saved account. A reserved/example address is refused.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Email    string `json:"email"`
			AgreeTOS bool   `json:"agree_tos"`
			Staging  bool   `json:"staging"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if !in.AgreeTOS {
			return nil, actions.InvalidParams("agree_tos: you must accept the Terms of Service to register with Let's Encrypt")
		}
		in.Email = strings.ToLower(strings.TrimSpace(in.Email))
		if in.Email == "" || !emailRe.MatchString(in.Email) {
			return nil, actions.InvalidParams("email: enter a valid contact address for renewal notices")
		}
		if reservedEmailDomain(in.Email) {
			return nil, actions.InvalidParams("email: Let's Encrypt does not accept an address at a reserved/example domain (e.g. example.com); use a real address")
		}
		if !env.Runner.Available("certbot") {
			return nil, actions.Failed("certbot is not installed. Install it on the Packages page (package: certbot).", "")
		}
		server := acmeProd
		if in.Staging {
			server = acmeStaging
		}
		registered, _, _ := accountStatus(env, server)
		var args []string
		if registered {
			args = []string{"update_account", "--non-interactive", "-m", in.Email}
		} else {
			args = []string{"register", "--non-interactive", "--agree-tos", "-m", in.Email}
		}
		if in.Staging {
			args = append(args, "--staging")
		}
		out, err := env.Runner.RunWith(ctx, "certbot", execx.Opts{Timeout: 90 * time.Second}, args...)
		if err != nil {
			return nil, actions.Failed(acmeFailureHeadline(string(out)), host.Output(out))
		}
		reg, uri, email := accountStatus(env, server)
		return map[string]any{"registered": reg, "updated": registered, "email": email, "account_uri": uri}, nil
	}})
}
