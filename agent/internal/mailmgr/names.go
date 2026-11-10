package mailmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sort"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/ssl"
	"github.com/NimVor/nimvor/agent/internal/sslmgr"
)

// A domain's own mail names. By default every domain uses the server's host name for IMAP, SMTP and
// webmail, which has the server's certificate. Turning names on gives a domain mail.<domain> (IMAP and SMTP)
// and webmail.<domain> with a Let's Encrypt certificate of their own:
//
//   - the certificate is obtained with the usual webroot challenge, answered on port 80 by a NimVor Nginx
//     site for the two names (the names must already point at this server in DNS);
//   - Dovecot serves it by SNI (a local_name block in its own drop-in) and Postfix by SNI too
//     (tls_server_sni_maps, one file per certificate holding the key and the chain, refreshed by certbot's
//     deploy hook after every renewal);
//   - webmail.<domain> is served by Roundcube through its own Nginx server blocks with that certificate.
//
// Nothing about the server's own host name changes: it stays the mail exchanger and the default certificate.

// DomainNames are the mail names of one domain.
type DomainNames struct {
	// Cert is the certbot certificate name (mail.<domain>); the certificate covers both names.
	Cert    string `json:"cert"`
	Mail    string `json:"mail"`
	Webmail string `json:"webmail"`
}

func namesFor(domain string) DomainNames {
	return DomainNames{Cert: "mail." + domain, Mail: "mail." + domain, Webmail: "webmail." + domain}
}

func leLive(cert string) string { return "/etc/letsencrypt/live/" + cert }

func (e env) namesNginxFile() string {
	return filepath.Join(e.M.NginxAvailable, "nimvor-mail-names.conf")
}
func (e env) namesNginxLink() string {
	return filepath.Join(e.M.NginxEnabled, "nimvor-mail-names.conf")
}
func (e env) namesDovecotFile() string {
	return filepath.Join(filepath.Dir(e.M.DovecotDropIn), "99-nimvor-names.conf")
}
func (e env) sniDir() string     { return e.M.MailDir + "/sni" }
func (e env) sniMapFile() string { return e.M.MailDir + "/sni_maps" }

// certReady says whether the certificate of the names exists and covers both of them.
func (e env) certReady(n DomainNames) (ssl.Certificate, bool) {
	live := leLive(n.Cert)
	if !e.Root.Exists(live+"/fullchain.pem") || !e.Root.Exists(live+"/privkey.pem") {
		return ssl.Certificate{}, false
	}
	c := ssl.Inspect(e.Root, live+"/fullchain.pem", live+"/privkey.pem", e.Now())
	return c, c.Error == "" && !c.Expired && c.Covers(n.Mail) && c.Covers(n.Webmail)
}

// namesDomains lists the domains with names turned on, in a stable order.
func namesDomains(d Data) []string {
	var out []string
	for dom, v := range d.Domains {
		if v.Names != nil {
			out = append(out, dom)
		}
	}
	sort.Strings(out)
	return out
}

func renderDovecotNames(d Data, ready func(DomainNames) bool) string {
	var b strings.Builder
	b.WriteString("# Managed by NimVor (email → mail names). Do not edit: changes are overwritten.\n")
	for _, dom := range namesDomains(d) {
		n := *d.Domains[dom].Names
		if !ready(n) {
			continue
		}
		live := leLive(n.Cert)
		if d.Dovecot24 {
			fmt.Fprintf(&b, "local_name %s {\n  ssl_server_cert_file = %s/fullchain.pem\n  ssl_server_key_file = %s/privkey.pem\n}\n", n.Mail, live, live)
		} else {
			fmt.Fprintf(&b, "local_name %s {\n  ssl_cert = <%s/fullchain.pem\n  ssl_key = <%s/privkey.pem\n}\n", n.Mail, live, live)
		}
	}
	return b.String()
}

// renderNamesVhost is the Nginx file of all domains' mail names: port 80 answers the certificate challenge
// for the names that have no certificate yet (and mail.<domain>, which has nothing to show), and webmail.<domain>
// is Roundcube over HTTPS once it has its certificate and webmail is on.
func (e env) renderNamesVhost(d Data, w Webmail, http2 bool) string {
	var b strings.Builder
	acme := "    location ^~ /.well-known/acme-challenge/ {\n        root " + e.M.ACMERoot + ";\n        default_type \"text/plain\";\n        try_files $uri =404;\n    }\n"
	b.WriteString("# Managed by NimVor (email → mail names). Do not edit: changes are overwritten.\n")
	for _, dom := range namesDomains(d) {
		n := *d.Domains[dom].Names
		_, ready := e.certReady(n)
		serveWebmail := ready && w.Enabled
		names := n.Mail
		if !serveWebmail {
			names += " " + n.Webmail
		}
		b.WriteString("\nserver {\n    listen 80;\n")
		if e.IPv6() {
			b.WriteString("    listen [::]:80;\n")
		}
		fmt.Fprintf(&b, "    server_name %s;\n%s    location / {\n        return 404;\n    }\n}\n", names, acme)
		if serveWebmail {
			live := leLive(n.Cert)
			b.WriteString("\n" + e.renderWebmailVhost(vhostParams{Hostname: n.Webmail, Cert: live + "/fullchain.pem", Key: live + "/privkey.pem", HTTP2Direct: http2, IPv6: e.IPv6()}))
		}
	}
	return b.String()
}

// applyNames writes everything the mail names need from the data, validates it, reloads the services and
// restores the previous state if any step fails.
func (e env) applyNames(ctx context.Context, d Data) error {
	w := e.loadWebmail()
	mainCf := filepath.Join(e.M.PostfixDir, "main.cf")
	before, _ := e.Root.ReadFile(mainCf)
	set := e.Changes("mail-names")
	e.writeData(set, d)

	set.Mkdir(e.sniDir(), 0o700, agentfs.RootOwner)
	var sniLines strings.Builder
	sniLines.WriteString(header)
	want := map[string]bool{}
	for _, dom := range namesDomains(d) {
		n := *d.Domains[dom].Names
		if _, ok := e.certReady(n); !ok {
			continue
		}
		live := leLive(n.Cert)
		key, kerr := e.Root.ReadFile(live + "/privkey.pem")
		chain, cerr := e.Root.ReadFile(live + "/fullchain.pem")
		if kerr != nil || cerr != nil {
			continue
		}
		pem := e.sniDir() + "/" + n.Cert + ".pem"
		want[pem] = true
		// Postfix reads the private key followed by the certificate chain from one file.
		set.Write(pem, append(append(append([]byte{}, key...), '\n'), chain...), 0o600, agentfs.RootOwner)
		fmt.Fprintf(&sniLines, "%s %s\n", n.Mail, pem)
	}
	if ents, err := e.Root.ReadDir(e.sniDir()); err == nil {
		for _, f := range ents {
			if p := e.sniDir() + "/" + f.Name(); strings.HasSuffix(f.Name(), ".pem") && !want[p] {
				set.Remove(p)
			}
		}
	}
	set.Write(e.sniMapFile(), []byte(sniLines.String()), 0o644, agentfs.RootOwner)
	set.Write(e.namesDovecotFile(), []byte(renderDovecotNames(d, func(n DomainNames) bool { _, ok := e.certReady(n); return ok })), 0o644, agentfs.RootOwner)

	hasNames := len(namesDomains(d)) > 0
	nginxChanged := hasNames || e.Root.Exists(e.namesNginxFile())
	if hasNames {
		if !e.Root.Exists(e.M.NginxAvailable) {
			set.Mkdir(e.M.NginxAvailable, 0o755, agentfs.RootOwner)
		}
		set.Write(e.namesNginxFile(), []byte(e.renderNamesVhost(d, w, e.NginxAtLeast(ctx, 1, 25, 1))), 0o644, agentfs.RootOwner)
		set.Link(e.namesNginxFile(), e.namesNginxLink())
	} else if e.Root.Exists(e.namesNginxFile()) {
		set.Remove(e.namesNginxLink())
		set.Remove(e.namesNginxFile())
	}
	sniOn := want != nil && len(want) > 0
	sniValue := ""
	if sniOn {
		sniValue = "texthash:" + e.sniMapFile()
	}

	restorePostfix := func() {
		if before != nil {
			e.Writer.WriteFile(mainCf, before, 0o644, agentfs.Keep)
		}
	}
	applied, err := set.Commit(func() error {
		if sniOn || e.postfixHasSNI(ctx) {
			if err := e.run(ctx, "postconf", "-e", "tls_server_sni_maps="+sniValue); err != nil {
				return err
			}
		}
		if err := e.run(ctx, "postfix", "check"); err != nil {
			return err
		}
		if err := e.run(ctx, "doveconf", "-n"); err != nil {
			return err
		}
		if nginxChanged {
			return e.NginxTest(ctx)
		}
		return nil
	})
	if err != nil {
		restorePostfix()
		return host.FailWithValidation("The mail names were not applied", err)
	}
	reload := func() error {
		if err := e.reloadData(ctx); err != nil {
			return err
		}
		if e.UnitActive(ctx, "dovecot.service") {
			if err := e.Systemctl(ctx, "reload", "dovecot.service"); err != nil {
				return err
			}
		}
		if nginxChanged && e.UnitActive(ctx, e.M.NginxUnit) {
			return e.ReloadNginx(ctx)
		}
		return nil
	}
	if err := reload(); err != nil {
		applied.Rollback()
		restorePostfix()
		reload()
		return actions.Failed("A service did not reload; the previous configuration was restored.", err.Error())
	}
	return nil
}

// postfixHasSNI says whether this Postfix knows tls_server_sni_maps (3.4 and later).
func (e env) postfixHasSNI(ctx context.Context) bool {
	out, err := e.Runner.Run(ctx, "postconf", "-h", "tls_server_sni_maps")
	return err == nil && !strings.Contains(host.Output(out), "unknown parameter")
}

func (e env) namesInfo(d Data, dom string) map[string]any {
	info := map[string]any{"domain": dom, "enabled": false, "mail_host": "mail." + dom, "webmail_host": "webmail." + dom,
		"sni_supported": true, "webmail_enabled": e.loadWebmail().Enabled}
	v, ok := d.Domains[dom]
	if !ok || v.Names == nil {
		return info
	}
	info["enabled"] = true
	n := *v.Names
	info["mail_host"], info["webmail_host"] = n.Mail, n.Webmail
	if c, ready := e.certReady(n); ready {
		info["cert_ready"], info["cert_expires"], info["cert_days"] = true, c.NotAfter, c.DaysRemaining
	} else {
		info["cert_ready"] = false
	}
	return info
}

// namesConflict returns a message when a website already answers for one of the names.
func (e env) namesConflict(ctx context.Context, n DomainNames) string {
	for _, w := range e.Discovery.Websites(ctx).Websites {
		mine := false
		for _, f := range w.ConfigFiles {
			mine = mine || f == e.namesNginxFile() || f == e.namesNginxLink()
		}
		if mine {
			continue
		}
		for _, name := range []string{n.Mail, n.Webmail} {
			if w.Domain == name || contains(w.Aliases, name) {
				return "The website " + w.Domain + " already answers for " + name + ". Remove that name from the website first."
			}
		}
	}
	return ""
}

func registerNames(r *actions.Registry, e env) {
	type req struct {
		Domain   string `json:"domain"`
		Email    string `json:"email"`
		Provider string `json:"provider"`
	}
	check := func(raw json.RawMessage) (req, error) {
		var in req
		if err := host.Decode(raw, &in); err != nil {
			return in, err
		}
		in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
		if !ValidDomain(in.Domain) {
			return in, actions.InvalidParams("domain is not a valid domain name")
		}
		return in, nil
	}

	r.Register(actions.Action{Name: "mail.names_status", Description: "A mail domain's own mail names (mail.<domain>, webmail.<domain>): whether they are on and whether their certificate is in place.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		in, err := check(raw)
		if err != nil {
			return nil, err
		}
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		if _, ok := d.Domains[in.Domain]; !ok {
			return nil, actions.NotFound("email is not hosted for " + in.Domain)
		}
		info := e.namesInfo(d, in.Domain)
		info["sni_supported"] = e.postfixHasSNI(ctx)
		return info, nil
	}})

	r.Register(actions.Action{Name: "mail.names_enable", Mutating: true, Description: "Give a mail domain its own mail names, mail.<domain> for IMAP and SMTP and webmail.<domain>, with a Let's Encrypt certificate (job). The names must already point at this server. Dovecot and Postfix serve the certificate by SNI and Nginx serves webmail on it; the server's own host name is unchanged. Rolled back if the certificate or the configuration fails.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		in, err := check(raw)
		if err != nil {
			return nil, err
		}
		if !e.configured() {
			return nil, actions.Conflict("Set up email first (Server → Email).")
		}
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		if _, ok := d.Domains[in.Domain]; !ok {
			return nil, actions.NotFound("email is not hosted for " + in.Domain)
		}
		n := namesFor(in.Domain)
		if msg := e.namesConflict(ctx, n); msg != "" {
			return nil, actions.Conflict(msg)
		}
		if !e.Runner.Available("certbot") {
			return nil, actions.Failed("certbot is not installed. Install it on the Packages page (package: certbot).", "")
		}
		for _, name := range []string{n.Mail, n.Webmail} {
			if !e.Cfg.SimulateCommands {
				if addrs, err := net.DefaultResolver.LookupHost(ctx, name); err != nil || len(addrs) == 0 {
					return nil, actions.Conflict(name + " does not resolve yet. Create the DNS records for " + n.Mail + " and " + n.Webmail + " (they must point to this server) and try again.")
				}
			}
		}
		j := e.Jobs.Start("mail.names_enable", "mail:names:"+in.Domain, func(ctx context.Context, log io.Writer) (any, error) {
			mu.Lock()
			defer mu.Unlock()
			defer e.LockWebConfig()()
			d, err := e.load()
			if err != nil {
				return nil, err
			}
			dv, ok := d.Domains[in.Domain]
			if !ok {
				return nil, fmt.Errorf("email is no longer hosted for %s", in.Domain)
			}
			prev := dv.Names
			dv.Names = &n
			d.Domains[in.Domain] = dv
			fmt.Fprintf(log, "Nginx answers the certificate check for %s and %s on port 80…\n", n.Mail, n.Webmail)
			if err := e.applyNames(ctx, d); err != nil {
				return nil, err
			}
			undo := func() {
				dv.Names = prev
				d.Domains[in.Domain] = dv
				e.applyNames(ctx, d)
			}
			fmt.Fprintf(log, "Asking for the certificate (this can take a minute)…\n")
			if _, err := sslmgr.IssueNames(ctx, e.Env, n.Cert, []string{n.Mail, n.Webmail}, in.Email, in.Provider, log); err != nil {
				fmt.Fprintln(log, "The certificate was not issued. The names must point to this server and port 80 must be reachable from the internet.")
				undo()
				return nil, err
			}
			fmt.Fprintln(log, "Certificate issued. Switching Dovecot, Postfix and webmail to it…")
			if err := e.applyNames(ctx, d); err != nil {
				undo()
				return nil, err
			}
			return e.namesInfo(d, in.Domain), nil
		})
		return map[string]any{"job_id": j.ID, "mail_host": n.Mail, "webmail_host": n.Webmail}, nil
	}})

	r.Register(actions.Action{Name: "mail.names_disable", Mutating: true, Description: "Remove a mail domain's own mail names: the SNI entries and webmail's host for it go away and the domain uses the server's host name again. The certificate stays (certbot keeps renewing it until it is deleted).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		in, err := check(raw)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		defer e.LockWebConfig()()
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		dv, ok := d.Domains[in.Domain]
		if !ok {
			return nil, actions.NotFound("email is not hosted for " + in.Domain)
		}
		if dv.Names == nil {
			return e.namesInfo(d, in.Domain), nil
		}
		dv.Names = nil
		d.Domains[in.Domain] = dv
		if err := e.applyNames(ctx, d); err != nil {
			return nil, err
		}
		return e.namesInfo(d, in.Domain), nil
	}})
}
