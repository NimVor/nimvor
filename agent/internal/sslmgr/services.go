package sslmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/changeset"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/ssl"
	"github.com/NimVor/nimvor/agent/internal/tlsaudit"
	"github.com/NimVor/nimvor/agent/internal/tlsprofile"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Certificates and TLS of WhaCP's own services (SSL/TLS → Services): the
// panel's HTTPS site (written by the installer), the mail server (Postfix
// SMTP and submission, Dovecot IMAP) and a Let's Encrypt certificate for
// the server's host name. Websites and webmail have their own pages.

var (
	certLineRe = regexp.MustCompile(`(?m)^(\s*)ssl_certificate\s+[^;\n]+;`)
	keyLineRe  = regexp.MustCompile(`(?m)^(\s*)ssl_certificate_key\s+[^;\n]+;`)
	// The installer's single TLS line in the panel's site, replaced by the
	// server-wide TLS snippet so the panel follows SSL/TLS → TLS configuration.
	panelProtoRe = regexp.MustCompile(`(?m)^\s*ssl_protocols\s+[^;\n]+;\n`)
)

func panelSite(env *host.Env) string { return filepath.Join(env.M.NginxAvailable, "whacp-panel.conf") }

func hostnameSite(env *host.Env) string {
	return filepath.Join(env.M.NginxAvailable, "whacp-hostname.conf")
}

func hostnameLink(env *host.Env) string {
	return filepath.Join(env.M.NginxEnabled, "whacp-hostname.conf")
}

func serverHostname(env *host.Env) string {
	for _, p := range []string{"/etc/hostname", "/proc/sys/kernel/hostname"} {
		if b, err := env.Root.ReadFile(p); err == nil && strings.TrimSpace(string(b)) != "" {
			return strings.ToLower(strings.TrimSpace(string(b)))
		}
	}
	if h, err := os.Hostname(); err == nil && env.Cfg.RootDir == "/" {
		return strings.ToLower(h)
	}
	return ""
}

// panelCert reads the certificate lines of the panel's site.
func panelCert(env *host.Env) (cert, key string, conf string, ok bool) {
	b, err := env.Root.ReadFile(panelSite(env))
	if err != nil {
		return "", "", "", false
	}
	conf = string(b)
	if m := certLineRe.FindString(conf); m != "" {
		cert = strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m), "ssl_certificate")), ";")
	}
	if m := keyLineRe.FindString(conf); m != "" {
		key = strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m), "ssl_certificate_key")), ";")
	}
	return cert, key, conf, cert != ""
}

func mailCert(ctx context.Context, env *host.Env) (cert, key string) {
	get := func(k string) string {
		out, err := env.Runner.Run(ctx, "postconf", "-h", k)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return get("smtpd_tls_cert_file"), get("smtpd_tls_key_file")
}

// mailConfigured: WhaCP set up the mail server (its Dovecot drop-in exists).
func mailConfigured(env *host.Env) bool { return env.Root.Exists(env.M.DovecotDropIn) }

// lineage is a certbot certificate by name.
func lineage(env *host.Env, name string) (cert, key string, err error) {
	if validate.Domain("cert_name", name) != nil {
		return "", "", actions.InvalidParams("cert_name: a Let's Encrypt certificate name")
	}
	live := filepath.Join(env.M.LiveDir(), name)
	cert, key = filepath.Join(live, "fullchain.pem"), filepath.Join(live, "privkey.pem")
	if !env.Root.Exists(cert) || !env.Root.Exists(key) {
		return "", "", actions.NotFound("There is no Let's Encrypt certificate named " + name + ".")
	}
	return cert, key, nil
}

func registerServices(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "ssl.services", Description: "Certificates and TLS of WhaCP's own services: the panel's HTTPS site and the mail server (Postfix and Dovecot), the server's host name and the Let's Encrypt certificates that could be used.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		now := env.Now()
		out := map[string]any{"hostname": serverHostname(env), "mail_tls": tlsprofile.LoadMail(env)}
		if c, k, conf, ok := panelCert(env); ok {
			out["panel"] = map[string]any{"certificate": ssl.Inspect(env.Root, c, k, now), "follows_tls_profile": strings.Contains(conf, tlsprofile.Path(env))}
		}
		if mailConfigured(env) {
			c, k := mailCert(ctx, env)
			if c != "" {
				out["mail"] = map[string]any{"certificate": ssl.Inspect(env.Root, c, k, now)}
			}
		}
		// Let's Encrypt certificates, with their names.
		var les []map[string]any
		dirs, _ := env.Root.ReadDir(env.M.LiveDir())
		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			live := filepath.Join(env.M.LiveDir(), d.Name())
			c := ssl.Inspect(env.Root, filepath.Join(live, "fullchain.pem"), filepath.Join(live, "privkey.pem"), now)
			if c.Error == "" {
				les = append(les, map[string]any{"name": d.Name(), "names": append([]string{c.CommonName}, c.DNSNames...), "days_remaining": c.DaysRemaining})
			}
		}
		out["lets_encrypt"] = les
		out["certbot_installed"] = env.Runner.Available("certbot")
		return out, nil
	}})

	r.Register(actions.Action{Name: "ssl.service_cert", Timeout: 3 * time.Minute, Mutating: true, Description: "Use a Let's Encrypt certificate for the panel's HTTPS site or the mail server (Postfix and Dovecot), or go back to the previous one (cert_name empty). Checked (nginx -t, postfix check, doveconf), reloaded, rolled back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Service  string `json:"service"`
			CertName string `json:"cert_name"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		certs := tlsprofile.LoadServiceCerts(env)
		cur := certs[in.Service]
		var cert, key string
		if in.CertName == "" {
			if cur.PrevCert == "" {
				return nil, actions.Conflict("There is no earlier certificate to go back to.")
			}
			cert, key = cur.PrevCert, cur.PrevKey
		} else {
			var err error
			if cert, key, err = lineage(env, in.CertName); err != nil {
				return nil, err
			}
		}
		switch in.Service {
		case "panel":
			return installPanelCert(ctx, env, certs, cert, key, in.CertName == "")
		case "mail":
			return installMailCert(ctx, env, certs, cert, key, in.CertName == "")
		}
		return nil, actions.InvalidParams("service: panel or mail")
	}})

	r.Register(actions.Action{Name: "ssl.mail_tls", Timeout: 3 * time.Minute, Mutating: true, Description: "The mail server's TLS: protocol versions (tls12: TLS 1.2 and 1.3; tls13: TLS 1.3 only) for IMAP and submission, and ciphers (default or Mozilla intermediate). Checked, reloaded, rolled back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var m tlsprofile.Mail
		if err := host.Decode(raw, &m); err != nil {
			return nil, err
		}
		if err := m.Validate(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		if !mailConfigured(env) {
			return nil, actions.Conflict("Set up email first (Email → Mail server).")
		}
		return applyMail(ctx, env, func(set *changeset.Set) error {
			tlsprofile.SaveMail(env, set, m)
			return nil
		}, m, "", "")
	}})

	r.Register(actions.Action{Name: "ssl.hostname_issue", Mutating: true, Description: "Obtain a Let's Encrypt certificate for the server's host name as a job (certbot's output in the job log). An Nginx site answers the challenge on port 80 when no website uses the name. Use it for the panel or the mail server afterwards.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Email    string `json:"email"`
			Provider string `json:"provider"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		p, ok := provider(in.Provider)
		if !ok {
			return nil, actions.InvalidParams("provider: unknown certificate authority")
		}
		name := serverHostname(env)
		if validate.Domain("hostname", name) != nil || !strings.Contains(name, ".") {
			return nil, actions.Conflict("The host name (" + name + ") is not a full domain name; set one such as server.example.com on the Server page first.")
		}
		if !env.Runner.Available("certbot") {
			return nil, actions.Failed("certbot is not installed. Install it on the Packages page (package: certbot).", "")
		}
		j := env.Jobs.Start("ssl.hostname_issue", "ssl:"+name, func(ctx context.Context, log io.Writer) (any, error) {
			fmt.Fprintf(log, "Host name: %s\n", name)
			if err := ensureHostnameSite(ctx, env, name); err != nil {
				return nil, err
			}
			fmt.Fprintf(log, "Nginx answers the %s check for the host name on port 80.\n", p.Label)
			fmt.Fprintf(log, "Asking %s (this can take a minute)…\n", p.Label)
			res, err := Issue(ctx, env, issueReq{CertName: name, Names: []string{name}, Email: in.Email, Provider: p.ID}, log)
			if err != nil {
				fmt.Fprintf(log, "%s did not issue the certificate. The host name must point to this server (its A record) and port 80 must be reachable from the internet.\n", p.Label)
				return nil, err
			}
			fmt.Fprintf(log, "Certificate issued: %s\nUse it for the panel or the mail server on SSL/TLS → Services SSL.\n", res["cert_file"])
			return res, nil
		})
		return map[string]any{"job_id": j.ID, "hostname": name}, nil
	}})

	r.Register(actions.Action{Name: "tls.audit", Timeout: 3 * time.Minute, Description: "Probe this server's own TLS endpoints and report weak protocols (TLS 1.0/1.1 accepted), weak cipher suites, and certificate problems (SHA-1, expired, untrusted chain, host-name mismatch). Read-only; it never changes any configuration. The panel passes the website and mail endpoints to check; each is dialed on this server (loopback), with the given name used for SNI and host-name verification.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Endpoints []tlsaudit.Endpoint `json:"endpoints"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		eps := make([]tlsaudit.Endpoint, 0, len(in.Endpoints))
		for _, ep := range in.Endpoints {
			if ep.Port < 1 || ep.Port > 65535 {
				return nil, actions.InvalidParams("each endpoint needs a port between 1 and 65535")
			}
			if ep.ServerName != "" && !hostnameRe.MatchString(ep.ServerName) {
				return nil, actions.InvalidParams("server_name must be a host name")
			}
			if ep.StartTLS != "" && ep.StartTLS != "smtp" {
				return nil, actions.InvalidParams("starttls must be empty or \"smtp\"")
			}
			// The agent only audits its own server: always dial loopback, so
			// it can never be used to probe a third party.
			ep.Host = ""
			eps = append(eps, ep)
		}
		if len(eps) == 0 {
			return nil, actions.InvalidParams("no endpoints to audit")
		}
		results := tlsaudit.Audit(ctx, eps)
		n := map[string]int{"high": 0, "medium": 0}
		for _, res := range results {
			for _, f := range res.Findings {
				n[f.Severity]++
			}
		}
		return map[string]any{"results": results, "high": n["high"], "medium": n["medium"]}, nil
	}})
}

// hostnameRe accepts a DNS host name for SNI (letters, digits, hyphen, dots).
var hostnameRe = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,62}\.?)+[a-z0-9]$`)

// ensureHostnameSite makes Nginx answer the ACME challenge for the host
// name on port 80, unless a website already serves it (WhaCP's websites
// all answer the challenge).
func ensureHostnameSite(ctx context.Context, env *host.Env, name string) error {
	// A website that already serves this name answers the challenge itself.
	for _, w := range env.Discovery.Websites(ctx).Websites {
		if w.Domain == name {
			return nil
		}
		for _, a := range w.Aliases {
			if a == name {
				return nil
			}
		}
	}
	listen := "    listen 80;\n"
	if env.IPv6() {
		listen += "    listen [::]:80;\n"
	}
	conf := "# Managed by WhaCP (SSL/TLS → Services): Let's Encrypt for the server's host name.\nserver {\n" + listen +
		"    server_name " + name + ";\n" +
		"    location ^~ /.well-known/acme-challenge/ {\n        root " + env.M.ACMERoot + ";\n        default_type \"text/plain\";\n        try_files $uri =404;\n    }\n" +
		"    location / {\n        return 404;\n    }\n}\n"
	// Idempotent only when the existing site already serves this exact name.
	// After a host-name change the old file names the previous host, so it
	// must be rewritten — otherwise Nginx no longer matches the new name and
	// the ACME challenge 404s.
	if b, err := env.Root.ReadFile(hostnameSite(env)); err == nil && string(b) == conf {
		return nil
	}
	defer env.LockWebConfig()()
	if err := ensureACMERoot(env); err != nil {
		return err
	}
	set := env.Changes("hostname-site")
	set.Write(hostnameSite(env), []byte(conf), 0o644, agentfs.RootOwner)
	set.Link(hostnameSite(env), hostnameLink(env))
	applied, err := set.Commit(func() error { return env.NginxTest(ctx) })
	if err != nil {
		return host.FailWithValidation("Nginx could not answer for the host name", err)
	}
	if err := env.ReloadNginx(ctx); err != nil {
		applied.Rollback()
		env.ReloadNginx(ctx)
		return host.FailWithValidation("Nginx did not reload", err)
	}
	return nil
}

func installPanelCert(ctx context.Context, env *host.Env, certs map[string]tlsprofile.ServiceCert, cert, key string, revert bool) (any, error) {
	oldCert, oldKey, conf, ok := panelCert(env)
	if !ok {
		return nil, actions.Conflict("The panel is not served over HTTPS by Nginx on this server (installed without --public-https).")
	}
	defer env.LockWebConfig()()
	next := certLineRe.ReplaceAllString(conf, "${1}ssl_certificate     "+cert+";")
	next = keyLineRe.ReplaceAllString(next, "${1}ssl_certificate_key "+key+";")
	// The panel follows the server-wide TLS configuration.
	if !strings.Contains(next, tlsprofile.Path(env)) {
		next = panelProtoRe.ReplaceAllString(next, tlsprofile.Include(env))
	}
	set := env.Changes("panel-cert")
	tlsprofile.Ensure(env, set)
	set.Write(panelSite(env), []byte(next), 0o644, agentfs.Keep)
	sc := certs["panel"]
	if revert {
		delete(certs, "panel")
	} else {
		if sc.PrevCert == "" {
			sc.PrevCert, sc.PrevKey = oldCert, oldKey
		}
		sc.Cert, sc.Key = cert, key
		certs["panel"] = sc
	}
	tlsprofile.SaveServiceCerts(env, set, certs)
	applied, err := set.Commit(func() error { return env.NginxTest(ctx) })
	if err != nil {
		return nil, host.FailWithValidation("The panel's certificate was not changed", err)
	}
	if err := env.ReloadNginx(ctx); err != nil {
		applied.Rollback()
		env.ReloadNginx(ctx)
		return nil, host.FailWithValidation("Nginx did not reload; the previous certificate was restored", err)
	}
	return map[string]any{"service": "panel", "certificate": ssl.Inspect(env.Root, cert, key, env.Now())}, nil
}

func installMailCert(ctx context.Context, env *host.Env, certs map[string]tlsprofile.ServiceCert, cert, key string, revert bool) (any, error) {
	if !mailConfigured(env) {
		return nil, actions.Conflict("Set up email first (Email → Mail server).")
	}
	oldCert, oldKey := mailCert(ctx, env)
	return applyMail(ctx, env, func(set *changeset.Set) error {
		sc := certs["mail"]
		if revert {
			delete(certs, "mail")
		} else {
			if sc.PrevCert == "" {
				sc.PrevCert, sc.PrevKey = oldCert, oldKey
			}
			sc.Cert, sc.Key = cert, key
			certs["mail"] = sc
		}
		tlsprofile.SaveServiceCerts(env, set, certs)
		return nil
	}, tlsprofile.LoadMail(env), cert, key)
}

// applyMail writes the mail server's TLS (profile m; the certificate when
// cert is set) into Dovecot's drop-in and Postfix, checks both, reloads
// them, and restores the previous files if anything fails.
func applyMail(ctx context.Context, env *host.Env, state func(*changeset.Set) error, m tlsprofile.Mail, cert, key string) (any, error) {
	b, err := env.Root.ReadFile(env.M.DovecotDropIn)
	if err != nil {
		return nil, actions.Failed("WhaCP's Dovecot configuration could not be read.", err.Error())
	}
	conf, err := tlsprofile.ReplaceDovecotLines(string(b), m)
	if err != nil {
		return nil, actions.Failed("WhaCP's Dovecot configuration has an unexpected form; set up email again first.", err.Error())
	}
	if cert != "" {
		conf = replaceDovecotCert(conf, cert, key)
	}
	mainCf := filepath.Join(env.M.PostfixDir, "main.cf")
	before, err := env.Root.ReadFile(mainCf)
	if err != nil {
		return nil, actions.Failed("Postfix's main.cf could not be read.", err.Error())
	}
	set := env.Changes("mail-tls")
	set.Write(env.M.DovecotDropIn, []byte(conf), 0o644, agentfs.RootOwner)
	if err := state(set); err != nil {
		return nil, err
	}
	settings := append([][2]string{{"smtpd_tls_mandatory_protocols", m.PostfixMandatoryProtocols()}}, m.PostfixCiphers()...)
	if cert != "" {
		settings = append(settings, [2]string{"smtpd_tls_cert_file", cert}, [2]string{"smtpd_tls_key_file", key})
	}
	restorePostfix := func() { env.Writer.WriteFile(mainCf, before, 0o644, agentfs.Keep) }
	applied, err := set.Commit(func() error {
		for _, kv := range settings {
			if out, err := env.Runner.Run(ctx, "postconf", "-e", kv[0]+"="+kv[1]); err != nil {
				return fmt.Errorf("postconf %s: %s", kv[0], host.Output(out))
			}
		}
		if out, err := env.Runner.Run(ctx, "postfix", "check"); err != nil {
			return fmt.Errorf("postfix check: %s", host.Output(out))
		}
		if out, err := env.Runner.Run(ctx, "doveconf", "-n"); err != nil {
			return fmt.Errorf("doveconf: %s", host.Output(out))
		}
		return nil
	})
	if err != nil {
		restorePostfix()
		return nil, host.FailWithValidation("The mail server's TLS was not changed", err)
	}
	for _, u := range []string{"postfix.service", "dovecot.service"} {
		if err := env.Systemctl(ctx, "reload", u); err != nil {
			applied.Rollback()
			restorePostfix()
			env.Systemctl(ctx, "reload", "postfix.service")
			env.Systemctl(ctx, "reload", "dovecot.service")
			return nil, actions.Failed("The mail server did not reload; the previous configuration was restored.", err.Error())
		}
	}
	return map[string]any{"service": "mail", "mail_tls": m}, nil
}

var (
	dovecotCert23 = regexp.MustCompile(`(?m)^ssl_cert = <\S+$`)
	dovecotKey23  = regexp.MustCompile(`(?m)^ssl_key = <\S+$`)
	dovecotCert24 = regexp.MustCompile(`(?m)^ssl_server_cert_file = \S+$`)
	dovecotKey24  = regexp.MustCompile(`(?m)^ssl_server_key_file = \S+$`)
)

func replaceDovecotCert(conf, cert, key string) string {
	conf = dovecotCert23.ReplaceAllString(conf, "ssl_cert = <"+cert)
	conf = dovecotKey23.ReplaceAllString(conf, "ssl_key = <"+key)
	conf = dovecotCert24.ReplaceAllString(conf, "ssl_server_cert_file = "+cert)
	return dovecotKey24.ReplaceAllString(conf, "ssl_server_key_file = "+key)
}
