// Package sslmgr issues, renews, installs and removes TLS certificates.
//
// Let's Encrypt certificates are obtained with certbot's webroot method
// using the shared ACME directory that every WhaCP site serves at
// /.well-known/acme-challenge/, so Nginx is never stopped or edited by
// certbot. Custom certificates are validated (parse, key match, expiry)
// before they are stored.
package sslmgr

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/ssl"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// DeployHook reloads Nginx after automatic renewals.
const DeployHook = "/etc/letsencrypt/renewal-hooks/deploy/whacp-reload-nginx.sh"

var emailRe = regexp.MustCompile(`^[A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9.-]{1,190}\.[A-Za-z]{2,}$`)

type issueReq struct {
	CertName string   `json:"cert_name"`
	Names    []string `json:"names"`
	Email    string   `json:"email"`
	Staging  bool     `json:"staging"`
}

type customReq struct {
	Name        string `json:"name"`
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
	Chain       string `json:"chain"`
	// KeyID installs against a key made on the server (instead of PrivateKey);
	// with UseSelfSigned its self-signed certificate is installed.
	KeyID         string `json:"key_id"`
	UseSelfSigned bool   `json:"use_self_signed"`
	// Domain, when set, must be covered by the certificate.
	Domain string `json:"domain"`
}

// Register adds certificate actions.
func Register(r *actions.Registry, env *host.Env) {
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, DeployHook)
	registerKeys(r, env)
	registerStore(r, env)

	r.Register(actions.Action{Name: "ssl.issue", Mutating: true, Timeout: 5 * time.Minute, Description: "Issue or expand a Let's Encrypt certificate (webroot).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in issueReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("cert_name", in.CertName); err != nil {
			return nil, host.Invalid(err)
		}
		if len(in.Names) == 0 || len(in.Names) > 100 {
			return nil, actions.InvalidParams("names: 1–100 domain names are required")
		}
		if err := validate.Domains("names", in.Names); err != nil {
			return nil, host.Invalid(err)
		}
		if in.Email != "" && !emailRe.MatchString(in.Email) {
			return nil, actions.InvalidParams("email: not a valid address")
		}
		if !env.Runner.Available("certbot") {
			return nil, actions.Failed("certbot is not installed. Install it on the Packages page (package: certbot).", "")
		}
		if err := ensureACMERoot(env); err != nil {
			return nil, err
		}
		if err := ensureDeployHook(env); err != nil {
			env.Log.Warn("could not install certbot deploy hook", "error", err)
		}
		args := []string{"certonly", "--webroot", "--webroot-path", env.M.ACMERoot, "--cert-name", in.CertName,
			"--non-interactive", "--agree-tos", "--keep-until-expiring", "--expand", "--no-eff-email"}
		if in.Email != "" {
			args = append(args, "--email", in.Email)
		} else {
			args = append(args, "--register-unsafely-without-email")
		}
		if in.Staging {
			args = append(args, "--staging")
		}
		for _, n := range in.Names {
			args = append(args, "-d", n)
		}
		out, err := env.Runner.RunWith(ctx, "certbot", execx.Opts{Timeout: 4 * time.Minute}, args...)
		if err != nil {
			return nil, actions.Failed("Let's Encrypt did not issue the certificate. Check that every domain's DNS points to this server and that port 80 is reachable.", host.Output(out))
		}
		live := filepath.Join(env.M.LiveDir(), in.CertName)
		c := ssl.Inspect(env.Root, filepath.Join(live, "fullchain.pem"), filepath.Join(live, "privkey.pem"), env.Now())
		return map[string]any{"cert_file": c.File, "key_file": c.KeyFile, "certificate": c}, nil
	}})

	r.Register(actions.Action{Name: "ssl.renew", Mutating: true, Timeout: 5 * time.Minute, Description: "Force renewal of a certbot certificate and reload Nginx.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			CertName string `json:"cert_name"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("cert_name", in.CertName); err != nil {
			return nil, host.Invalid(err)
		}
		out, err := env.Runner.RunWith(ctx, "certbot", execx.Opts{Timeout: 4 * time.Minute}, "renew", "--cert-name", in.CertName, "--force-renewal", "--non-interactive")
		if err != nil {
			return nil, actions.Failed("The certificate could not be renewed.", host.Output(out))
		}
		unlock := env.LockWebConfig()
		err = env.ReloadNginx(ctx)
		unlock()
		if err != nil {
			return nil, host.FailWithValidation("The certificate was renewed but Nginx was not reloaded", err)
		}
		live := filepath.Join(env.M.LiveDir(), in.CertName)
		return ssl.Inspect(env.Root, filepath.Join(live, "fullchain.pem"), filepath.Join(live, "privkey.pem"), env.Now()), nil
	}})

	r.Register(actions.Action{Name: "ssl.delete", Mutating: true, Description: "Delete a certbot certificate that no WhaCP site uses any more.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			CertName string `json:"cert_name"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("cert_name", in.CertName); err != nil {
			return nil, host.Invalid(err)
		}
		live := filepath.Join(env.M.LiveDir(), in.CertName)
		if users := referencing(ctx, env, live); len(users) > 0 {
			return nil, actions.Conflict("The certificate is still used by: " + strings.Join(users, ", ") + ". Disable HTTPS there first.")
		}
		out, err := env.Runner.Run(ctx, "certbot", "delete", "--cert-name", in.CertName, "--non-interactive")
		if err != nil {
			return nil, actions.Failed("The certificate could not be deleted.", host.Output(out))
		}
		return map[string]any{"deleted": in.CertName}, nil
	}})

	r.Register(actions.Action{Name: "ssl.custom", Mutating: true, Description: "Install an uploaded certificate and private key after validating them.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in customReq
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("name", in.Name); err != nil {
			return nil, host.Invalid(err)
		}
		if in.KeyID != "" {
			if in.PrivateKey != "" {
				return nil, actions.InvalidParams("give either a private key or key_id, not both")
			}
			key, err := readStoredKey(env, in.KeyID)
			if err != nil {
				return nil, err
			}
			in.PrivateKey = key
			if in.UseSelfSigned {
				m, err := readKeyMeta(env, in.KeyID, true)
				if err != nil {
					return nil, err
				}
				if !m.SelfSigned {
					return nil, actions.InvalidParams("that key has no self-signed certificate")
				}
				in.Certificate, in.Chain = m.Certificate, ""
			}
		}
		info, fullchain, err := CheckCustom(in, env.Now())
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(env.Cfg.StateDir, "certs", in.Name)
		set := env.Changes("cert-" + in.Name)
		set.Mkdir(filepath.Join(env.Cfg.StateDir, "certs"), 0o700, agentfs.RootOwner)
		set.Mkdir(dir, 0o700, agentfs.RootOwner)
		set.Write(filepath.Join(dir, "fullchain.pem"), fullchain, 0o644, agentfs.RootOwner)
		set.Write(filepath.Join(dir, "privkey.pem"), []byte(strings.TrimSpace(in.PrivateKey)+"\n"), 0o600, agentfs.RootOwner)
		if _, err := set.Apply(); err != nil {
			return nil, host.FailWithValidation("The certificate was not stored", err)
		}
		return map[string]any{"cert_file": filepath.Join(dir, "fullchain.pem"), "key_file": filepath.Join(dir, "privkey.pem"), "certificate": info}, nil
	}})

	r.Register(actions.Action{Name: "ssl.inspect", Description: "Details of a certificate file used by a site.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			CertFile string `json:"cert_file"`
			KeyFile  string `json:"key_file"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if validate.Within("cert_file", "/etc/letsencrypt", in.CertFile) != nil && validate.Within("cert_file", filepath.Join(env.Cfg.StateDir, "certs"), in.CertFile) != nil {
			return nil, actions.InvalidParams("cert_file must be a certbot or WhaCP certificate")
		}
		return ssl.Inspect(env.Root, in.CertFile, in.KeyFile, env.Now()), nil
	}})

	r.Register(actions.Action{Name: "ssl.renewal_status", Description: "Whether automatic certificate renewal is active.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		status := map[string]any{"certbot_installed": env.Runner.Available("certbot"), "timer_active": false, "method": "none", "deploy_hook": env.Root.Exists(DeployHook)}
		for _, t := range []string{"certbot.timer", "snap.certbot.renew.timer"} {
			if env.UnitActive(ctx, t) {
				status["timer_active"], status["method"] = true, t
			}
		}
		if status["method"] == "none" && env.Root.Exists("/etc/cron.d/certbot") {
			status["timer_active"], status["method"] = true, "/etc/cron.d/certbot"
		}
		return status, nil
	}})
}

func ensureACMERoot(env *host.Env) error {
	set := env.Changes("acme-root")
	set.Mkdir(env.M.ACMERoot, 0o755, agentfs.RootOwner)
	_, err := set.Apply()
	if err != nil {
		return host.FailWithValidation("The ACME challenge directory could not be created", err)
	}
	return nil
}

func ensureDeployHook(env *host.Env) error {
	set := env.Changes("certbot-hook")
	set.Write(DeployHook, []byte("#!/bin/sh\n# Managed by WhaCP: reload Nginx after a certificate renewal, only if the configuration is valid.\nnginx -t -q && systemctl reload nginx\n"), 0o755, agentfs.RootOwner)
	_, err := set.Apply()
	return err
}

// referencing lists Nginx vhost files that use certificates under dir.
func referencing(ctx context.Context, env *host.Env, dir string) []string {
	var out []string
	for _, v := range env.Discovery.Nginx(ctx).VHosts {
		if v.CertFile != "" && strings.HasPrefix(v.CertFile, dir+"/") {
			out = append(out, filepath.Base(v.File))
		}
	}
	return out
}

// CheckCustom validates an uploaded certificate/key pair and returns the
// fullchain (leaf + chain) to store.
func CheckCustom(in customReq, now time.Time) (ssl.Certificate, []byte, error) {
	certPEM := strings.TrimSpace(in.Certificate)
	chainPEM := strings.TrimSpace(in.Chain)
	keyPEM := strings.TrimSpace(in.PrivateKey)
	if len(certPEM)+len(chainPEM)+len(keyPEM) > 256<<10 {
		return ssl.Certificate{}, nil, actions.InvalidParams("the certificate data is too large")
	}
	full := certPEM + "\n"
	if chainPEM != "" {
		full += chainPEM + "\n"
	}
	if strings.Contains(certPEM, "PRIVATE KEY") || strings.Contains(chainPEM, "PRIVATE KEY") {
		return ssl.Certificate{}, nil, actions.InvalidParams("the private key must be pasted in the private key field only")
	}
	pair, err := tls.X509KeyPair([]byte(full), []byte(keyPEM))
	if err != nil {
		return ssl.Certificate{}, nil, actions.InvalidParams("the private key does not match the certificate, or one of them is malformed")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return ssl.Certificate{}, nil, actions.InvalidParams("the certificate is malformed")
	}
	if now.After(leaf.NotAfter) {
		return ssl.Certificate{}, nil, actions.InvalidParams("the certificate has expired")
	}
	if block, _ := pem.Decode([]byte(keyPEM)); block == nil {
		return ssl.Certificate{}, nil, actions.InvalidParams("the private key is not PEM encoded")
	}
	if now.Before(leaf.NotBefore) {
		return ssl.Certificate{}, nil, actions.InvalidParams("the certificate is not valid yet (from " + leaf.NotBefore.UTC().Format("2006-01-02 15:04") + " UTC)")
	}
	if err := checkChain([]byte(full)); err != nil {
		return ssl.Certificate{}, nil, actions.InvalidParams(err.Error())
	}
	info := ssl.Certificate{
		CommonName: leaf.Subject.CommonName, DNSNames: leaf.DNSNames, Issuer: leaf.Issuer.String(),
		NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter, DaysRemaining: int(leaf.NotAfter.Sub(now).Hours() / 24),
		ChainLength: len(pair.Certificate), SelfSigned: leaf.Issuer.String() == leaf.Subject.String(),
	}
	if info.DNSNames == nil {
		info.DNSNames = []string{}
	}
	if in.Domain != "" && !info.Covers(in.Domain) {
		return ssl.Certificate{}, nil, actions.InvalidParams("the certificate is not valid for " + in.Domain + " (it covers " + strings.Join(append([]string{info.CommonName}, info.DNSNames...), ", ") + ")")
	}
	return info, []byte(full), nil
}
