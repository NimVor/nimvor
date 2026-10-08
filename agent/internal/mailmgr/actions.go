package mailmgr

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	cryptotls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/changeset"
	"github.com/mchkhawarshahzad/whacp/agent/internal/fw"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/limits"
	"github.com/mchkhawarshahzad/whacp/agent/internal/logging"
	"github.com/mchkhawarshahzad/whacp/agent/internal/selinux"
	"github.com/mchkhawarshahzad/whacp/agent/internal/shacrypt"
	"github.com/mchkhawarshahzad/whacp/agent/internal/tlsprofile"
)

var mu sync.Mutex

// probe checks a TCP service greeting (tests replace it).
var probe = func(ctx context.Context, addr, prefix string, implicitTLS bool) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	var last error
	for i := 0; i < 20; i++ {
		var c net.Conn
		var err error
		if implicitTLS {
			c, err = (&cryptotls.Dialer{NetDialer: &d, Config: &cryptotls.Config{InsecureSkipVerify: true}}).DialContext(ctx, "tcp", addr) //nolint:gosec // only checks the service answers
		} else {
			c, err = d.DialContext(ctx, "tcp", addr)
		}
		if err == nil {
			c.SetDeadline(time.Now().Add(5 * time.Second))
			line, rerr := bufio.NewReader(c).ReadString('\n')
			c.Close()
			if rerr == nil && strings.HasPrefix(line, prefix) {
				return nil
			}
			err = fmt.Errorf("unexpected greeting %q", strings.TrimSpace(line))
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("%s: %v", addr, last)
}

// startTLS checks that an SMTP server upgrades a connection to TLS 1.2 or
// later (tests replace it).
var startTLS = func(ctx context.Context, addr string) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	r := bufio.NewReader(c)
	reply := func() (string, error) {
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return line, err
			}
			if len(line) < 4 || line[3] != '-' {
				return line, nil
			}
		}
	}
	for _, step := range []struct{ send, want string }{{"", "220"}, {"EHLO whacp.local\r\n", "250"}, {"STARTTLS\r\n", "220"}} {
		if step.send != "" {
			if _, err := c.Write([]byte(step.send)); err != nil {
				return err
			}
		}
		line, err := reply()
		if err != nil || !strings.HasPrefix(line, step.want) {
			return fmt.Errorf("%sexpected %s, got %q", strings.TrimSpace(step.send+" "), step.want, strings.TrimSpace(line))
		}
	}
	tc := cryptotls.Client(c, &cryptotls.Config{InsecureSkipVerify: true, MinVersion: cryptotls.VersionTLS12}) //nolint:gosec // only checks the handshake
	return tc.HandshakeContext(ctx)
}

type env struct{ *host.Env }

func (e env) dataPath() string { return e.M.MailDir + "/mail.json" }

func (e env) load() (Data, error) {
	b, err := e.Root.ReadFile(e.dataPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return newData(), err
	}
	return parseData(b)
}

func (e env) installed() map[string]bool {
	return map[string]bool{
		"postfix":  e.Root.Exists("/usr/sbin/postfix"),
		"dovecot":  e.Root.Exists("/usr/sbin/dovecot"),
		"imap":     e.Root.Exists("/usr/lib/dovecot/imap") || e.Root.Exists("/usr/libexec/dovecot/imap"),
		"lmtp":     e.Root.Exists("/usr/lib/dovecot/lmtp") || e.Root.Exists("/usr/libexec/dovecot/lmtp"),
		"sieve":    e.Root.Exists("/usr/lib/dovecot/modules/lib90_sieve_plugin.so") || e.Root.Exists("/usr/lib64/dovecot/lib90_sieve_plugin.so"),
		"opendkim": e.Root.Exists("/usr/sbin/opendkim"),
	}
}

func (e env) configured() bool {
	return e.Root.Exists(e.M.DovecotDropIn) && e.Root.Exists(e.dataPath())
}

func (e env) owner(user string) agentfs.Owner {
	if u, err := e.LookupUser(user); err == nil {
		return agentfs.Owner{UID: u.UID, GID: u.GID}
	}
	return agentfs.RootOwner
}

func (e env) group(user string) agentfs.Owner {
	if u, err := e.LookupUser(user); err == nil {
		return agentfs.Owner{UID: 0, GID: u.GID}
	}
	return agentfs.RootOwner
}

// writeData stages mail.json and every derived file.
func (e env) writeData(set *changeset.Set, d Data) {
	b, _ := json.MarshalIndent(d, "", "  ")
	set.Write(e.dataPath(), b, 0o600, agentfs.RootOwner)
	for name, content := range d.render(e.M.MailDir, e.M.VmailDir) {
		switch {
		case name == "users":
			set.Write(e.M.MailDir+"/users", []byte(content), 0o640, e.group("dovecot"))
		case strings.HasPrefix(name, "dkim/"):
			set.Write(e.M.MailDir+"/"+name, []byte(content), 0o640, e.group("opendkim"))
		default:
			set.Write(e.M.MailDir+"/"+name, []byte(content), 0o644, agentfs.RootOwner)
		}
	}
	set.Write(e.M.MailDir+"/dkim/TrustedHosts", []byte("127.0.0.1\n::1\nlocalhost\n"), 0o640, e.group("opendkim"))
}

func (e env) run(ctx context.Context, name string, args ...string) error {
	out, err := e.Runner.Run(ctx, name, args...)
	if err != nil {
		return fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), host.Output(out))
	}
	return nil
}

// reloadPostfix makes Postfix use its changed configuration and returns
// once it does. "postfix reload" only signals the master process: an smtpd
// started before it can still take the next connection with the old
// settings (seen in tests: a message accepted right after the spam filter
// was turned on, without being scanned). New smtpd processes read main.cf
// themselves, so waiting for the earlier ones to exit is enough; a busy one
// finishes its session first, so the wait is bounded.
func (e env) reloadPostfix(ctx context.Context) error {
	before := e.smtpdPIDs()
	if err := e.run(ctx, "postfix", "reload"); err != nil {
		return err
	}
	if e.Cfg.SimulateCommands {
		return nil
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(before) > 0 && time.Now().Before(deadline) {
		for pid := range before {
			if _, err := os.Stat(filepath.Join(e.Cfg.ProcDir, pid)); err != nil {
				delete(before, pid)
			}
		}
		if len(before) > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	if len(before) > 0 {
		e.Log.Warn("smtpd processes from before the Postfix reload are still running (busy sessions)", "count", len(before))
	}
	return nil
}

// smtpdPIDs lists running smtpd processes.
func (e env) smtpdPIDs() map[string]bool {
	pids := map[string]bool{}
	entries, _ := os.ReadDir(e.Cfg.ProcDir)
	for _, d := range entries {
		if _, err := strconv.Atoi(d.Name()); err != nil {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(e.Cfg.ProcDir, d.Name(), "comm")); err == nil && strings.TrimSpace(string(b)) == "smtpd" {
			pids[d.Name()] = true
		}
	}
	return pids
}

// reloadData makes the services read the changed files.
func (e env) reloadData(ctx context.Context) error {
	if e.run(ctx, "postfix", "status") == nil {
		if err := e.reloadPostfix(ctx); err != nil {
			return err
		}
	}
	if e.UnitActive(ctx, DKIMUnit) {
		if err := e.Systemctl(ctx, "reload", DKIMUnit); err != nil {
			return err
		}
	}
	return nil
}

// saveData writes the data, checks Postfix accepts it and reloads.
func (e env) saveData(ctx context.Context, d Data, extra func(*changeset.Set)) error {
	set := e.Changes("mail")
	e.writeData(set, d)
	if extra != nil {
		extra(set)
	}
	applied, err := set.Commit(func() error { return e.run(ctx, "postfix", "check") })
	if err != nil {
		return host.FailWithValidation("The mail settings were not saved", err)
	}
	if err := e.reloadData(ctx); err != nil {
		applied.Rollback()
		e.reloadData(ctx)
		return actions.Failed("The mail server did not reload; the previous settings were restored.", err.Error())
	}
	return nil
}

func selfSigned(hostname string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: hostname}, DNSNames: []string{hostname},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	kd, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}), nil
}

var hostnameRe = domainRe

// Register adds the email actions.
func Register(r *actions.Registry, he *host.Env) {
	e := env{he}
	dir := he.M.MailDir
	registerSpam(r, e)
	registerWebmail(r, e)
	registerFilters(r, e)
	registerQueue(r, e)
	registerLogStats(r, e)
	registerManager(r, e)
	registerGreylist(r, e)
	registerMailRate(r, e)
	registerAuthCheck(r, e)
	registerTools(r, e)

	r.Register(actions.Action{Name: "mail.status", Description: "Email: installed components, whether set up and running, hosted domains (with DKIM records), mailboxes (with size) and aliases.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		d, _ := e.load()
		boxes := []map[string]any{}
		for _, addr := range sortedKeys(d.Mailboxes) {
			local, dom, _ := strings.Cut(addr, "@")
			size, _, _ := e.Root.DirSize(filepath.Join(e.M.VmailDir, dom, local), 200000)
			boxes = append(boxes, map[string]any{"address": addr, "quota_mb": d.Mailboxes[addr].QuotaMB, "bytes": size})
		}
		domains := []map[string]any{}
		for _, dom := range sortedKeys(d.Domains) {
			domains = append(domains, map[string]any{"domain": dom, "dkim_selector": d.Domains[dom].Selector, "dkim_txt": d.Domains[dom].DKIMPublic})
		}
		aliases := []map[string]any{}
		for _, src := range sortedKeys(d.Aliases) {
			aliases = append(aliases, map[string]any{"source": src, "destinations": d.Aliases[src]})
		}
		dovecotVersion := ""
		if out, err := e.Runner.Run(ctx, "dovecot", "--version"); err == nil {
			dovecotVersion = strings.Fields(string(out) + " ")[0]
		}
		return map[string]any{
			"installed": e.installed(), "configured": e.configured(), "hostname": d.Hostname, "dovecot_version": dovecotVersion,
			"running": map[string]bool{"postfix": e.run(ctx, "postfix", "status") == nil, "dovecot": e.UnitActive(ctx, "dovecot.service"), "opendkim": e.UnitActive(ctx, DKIMUnit)},
			"domains": domains, "mailboxes": boxes, "aliases": aliases,
			"spam": d.Spam, "spam_installed": e.rspamdInstalled(), "spam_running": e.UnitActive(ctx, rspamdUnit),
			"webmail": e.webmailStatus(ctx),
		}, nil
	}})

	r.Register(actions.Action{Name: "mail.configure", Mutating: true, Description: "Set up Postfix, Dovecot and OpenDKIM for WhaCP's mail domains (changes Postfix's main.cf/master.cf with postconf and disables Dovecot's system-account login; requires confirm_edit_package_config). Validated, restarted, checked on ports 25, 587 and 993, rolled back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Hostname string `json:"hostname"`
			Confirm  bool   `json:"confirm_edit_package_config"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		in.Hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Hostname), "."))
		if !hostnameRe.MatchString(in.Hostname) {
			return nil, actions.InvalidParams("hostname must be the mail server's full host name, e.g. mail.example.com")
		}
		mu.Lock()
		defer mu.Unlock()
		for name, ok := range e.installed() {
			if !ok {
				return nil, actions.Conflict("The mail packages are not complete (" + name + " is missing). Install Postfix, Dovecot (IMAP, LMTP and Sieve) and OpenDKIM under Server → Packages first.")
			}
		}
		v24 := false
		if out, err := e.Runner.Run(ctx, "dovecot", "--version"); err == nil {
			ver := strings.TrimSpace(string(out))
			if !strings.HasPrefix(ver, "2.3.") && !strings.HasPrefix(ver, "2.4.") && !he.Cfg.SimulateCommands {
				return nil, actions.Conflict("WhaCP supports Dovecot 2.3 and 2.4; this server has " + ver + ".")
			}
			v24 = dovecotV24(ver)
		}
		if !e.configured() && !in.Confirm {
			return nil, actions.Conflict("WhaCP changes Postfix's main.cf and master.cf (with postconf) and turns off Dovecot's login for system accounts in " + he.M.DovecotAuthConf + ". Confirm to continue; backups are kept.")
		}
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		d.Hostname = in.Hostname
		d.Dovecot24 = v24
		// vmail owns all mailboxes.
		if _, err := he.LookupUser("vmail"); err != nil {
			if err := e.run(ctx, "useradd", "--system", "--home-dir", e.M.VmailDir, "--no-create-home", "--shell", "/usr/sbin/nologin", "--user-group", "--comment", "WhaCP mailboxes", "--", "vmail"); err != nil {
				return nil, actions.Failed("Could not create the vmail account.", err.Error())
			}
		}
		vmail, err := he.LookupUser("vmail")
		if err != nil && !he.Cfg.SimulateCommands {
			return nil, actions.Failed("The vmail account is missing.", err.Error())
		}
		// Certificate: Let's Encrypt for the host name when present.
		t := tls{cert: dir + "/tls.crt", key: dir + "/tls.key"}
		le := "/etc/letsencrypt/live/" + in.Hostname
		if he.Root.Exists(le+"/fullchain.pem") && he.Root.Exists(le+"/privkey.pem") {
			t = tls{cert: le + "/fullchain.pem", key: le + "/privkey.pem"}
		}
		// A certificate chosen under SSL/TLS → Service certificates wins.
		if c, k, ok := tlsprofile.MailCert(he); ok {
			t = tls{cert: c, key: k}
		}
		mailTLS := tlsprofile.LoadMail(he)
		// Package files are snapshotted: postconf edits them in place.
		type snap struct {
			path string
			data []byte
			ok   bool
		}
		var snaps []snap
		for _, p := range []string{he.M.PostfixDir + "/main.cf", he.M.PostfixDir + "/master.cf"} {
			b, err := he.Root.ReadFile(p)
			snaps = append(snaps, snap{p, b, err == nil})
		}
		restorePostfix := func() {
			for _, s := range snaps {
				if s.ok {
					he.Writer.WriteFile(s.path, s.data, 0o644, agentfs.Keep)
				}
			}
		}

		set := he.Changes("mail-setup")
		set.Mkdir(dir, 0o755, agentfs.RootOwner)
		// Root-owned: OpenDKIM reads its tables as root before it drops to
		// its account (under SELinux the DKIM domain may not bypass file
		// permissions), then reads the keys through the group.
		set.Mkdir(dir+"/dkim", 0o750, e.group("opendkim"))
		set.Mkdir(e.M.VmailDir, 0o750, agentfs.Owner{UID: vmail.UID, GID: vmail.GID})
		if t.cert == dir+"/tls.crt" && !he.Root.Exists(t.cert) {
			c, k, err := selfSigned(in.Hostname)
			if err != nil {
				return nil, actions.Failed("Could not create the mail certificate.", err.Error())
			}
			set.Write(t.cert, c, 0o644, agentfs.RootOwner)
			set.Write(t.key, k, 0o600, agentfs.RootOwner)
		}
		e.writeData(set, d)
		set.Write(he.M.DovecotDropIn, []byte(renderDovecot(dovecotParams{Dir: dir, Vmail: e.M.VmailDir, Hostname: in.Hostname, VmailUID: vmail.UID, TLS: t, IPv6: hasIPv6(), V24: v24, TLSLines: mailTLS.DovecotLines()})), 0o644, agentfs.RootOwner)
		set.Mkdir(dir+"/sieve", 0o755, agentfs.RootOwner)
		set.Write(dir+"/sieve/spam.sieve", []byte(RenderSpamSieve(d.Spam.NoJunkDomains)), 0o644, agentfs.RootOwner)
		if b, err := he.Root.ReadFile(he.M.DovecotAuthConf); err == nil {
			if changed, ok := disableSystemAuth(string(b)); ok {
				set.Write(he.M.DovecotAuthConf, []byte(changed), 0o644, agentfs.Keep)
			}
		}
		set.Write(dir+"/opendkim.conf", []byte(renderDKIMConf(dir)), 0o644, agentfs.RootOwner)
		set.Write(filepath.Join(he.M.UnitDir, DKIMUnit), []byte(renderDKIMUnit(dir)), 0o644, agentfs.RootOwner)
		applied, err := set.Commit(func() error {
			// Compiled once here: Dovecot cannot write next to the script.
			if err := e.run(ctx, "sievec", dir+"/sieve/spam.sieve"); err != nil {
				return err
			}
			if out, err := he.Runner.Run(ctx, "doveconf", "-n"); err != nil {
				return fmt.Errorf("doveconf: %s", host.Output(out))
			}
			for _, kv := range postfixSettings(dir, in.Hostname, t, hasIPv6(), d.Spam.Enabled, mailTLS) {
				if err := e.run(ctx, "postconf", "-e", kv[0]+"="+kv[1]); err != nil {
					return err
				}
			}
			// Outgoing mail limits keep website accounts off Postfix's sendmail.
			if rc, err := e.loadMailRate(); err == nil && rc.Enabled && he.Root.Exists(filepath.Join(dir, limits.NoSubmitFile)) {
				if err := e.run(ctx, "postconf", "-e", "authorized_submit_users="+submitLimited(dir)); err != nil {
					return err
				}
			}
			// The administrator's settings win over the defaults above.
			if err := e.applyPostfixTuning(ctx, d.Tuning.Postfix); err != nil {
				return err
			}
			if err := e.run(ctx, "postconf", "-M", "submission/inet=submission inet n - y - - smtpd"); err != nil {
				return err
			}
			for _, kv := range submission {
				if err := e.run(ctx, "postconf", "-P", "submission/inet/"+kv[0]+"="+kv[1]); err != nil {
					return err
				}
			}
			return e.run(ctx, "postfix", "check")
		})
		if err != nil {
			restorePostfix()
			return nil, host.FailWithValidation("Email was not set up", err)
		}
		fail := func(msg string, cause error) (any, error) {
			applied.Rollback()
			restorePostfix()
			he.DaemonReload(ctx)
			he.Systemctl(ctx, "restart", "dovecot.service")
			he.Systemctl(ctx, "restart", "postfix.service")
			return nil, actions.Failed(msg+" The previous configuration was restored.", cause.Error())
		}
		// SELinux: Dovecot stores mail under VmailDir.
		if err := selinux.Label(ctx, he, e.M.VmailDir, "mail_spool_t"); err != nil {
			return fail("The mail storage could not be labelled for SELinux.", err)
		}
		// Postfix's smtpd (postfix_smtpd_t) reads TLS files only with the
		// certificate label; Dovecot reads it too.
		for _, f := range []string{t.cert, t.key} {
			if strings.HasPrefix(f, dir+"/") {
				if err := selinux.Label(ctx, he, f, "cert_t"); err != nil {
					he.Log.Warn("selinux: TLS file label", "file", f, "err", err)
				}
			}
		}
		// OpenDKIM (dkim_milter_t) reads its keys and tables only with the
		// label of its own key folder.
		if err := selinux.Label(ctx, he, dir+"/dkim", "dkim_milter_private_key_t"); err != nil {
			he.Log.Warn("selinux: DKIM folder label", "err", err)
		}
		if err := he.DaemonReload(ctx); err != nil {
			return fail("systemd could not load the DKIM service.", err)
		}
		he.Systemctl(ctx, "enable", DKIMUnit)
		for _, u := range []string{DKIMUnit, "dovecot.service", "postfix.service"} {
			he.Systemctl(ctx, "enable", u)
			if err := he.Systemctl(ctx, "restart", u); err != nil {
				return fail(u+" did not start.", err)
			}
		}
		if !he.Cfg.SimulateCommands {
			for _, p := range []struct {
				addr, greet string
				tls         bool
			}{{"127.0.0.1:25", "220", false}, {"127.0.0.1:587", "220", false}, {"127.0.0.1:993", "* OK", true}, {fmt.Sprintf("127.0.0.1:%d", dkimPort), "", false}} {
				if p.greet == "" {
					// A Type=simple unit is "started" before it listens.
					var err error
					for i := 0; i < 40; i++ {
						var c net.Conn
						if c, err = net.DialTimeout("tcp", p.addr, 2*time.Second); err == nil {
							c.Close()
							break
						}
						time.Sleep(250 * time.Millisecond)
					}
					if err != nil {
						return fail("OpenDKIM does not listen.", fmt.Errorf("%v\n%s\n%s", err, he.JournalTail(ctx, DKIMUnit, 8), he.Describe(dir, dir+"/opendkim.conf", dir+"/dkim", dir+"/dkim/TrustedHosts", dir+"/dkim/KeyTable", dir+"/dkim/SigningTable")))
					}
					continue
				}
				if err := probe(ctx, p.addr, p.greet, p.tls); err != nil {
					return fail("The mail server does not answer.", err)
				}
			}
			// Submission requires TLS: a Postfix that cannot start it
			// (unreadable key, unsupported setting) refuses every login.
			if err := startTLS(ctx, "127.0.0.1:587"); err != nil {
				return fail("The mail server cannot start TLS for submission.", fmt.Errorf("%v\n%s", err, he.JournalTail(ctx, "postfix.service", 10)))
			}
		}
		notes := []string{}
		notes = append(notes, fw.Open(ctx, he, "mail", "25/tcp", "587/tcp", "993/tcp"))
		if t.cert == dir+"/tls.crt" {
			notes = append(notes, "A self-signed certificate is used; issue a Let's Encrypt certificate for "+in.Hostname+" and set up email again to use it.")
		}
		return map[string]any{"configured": true, "hostname": in.Hostname, "certificate": t.cert, "notes": notes}, nil
	}})

	type domainReq struct {
		Domain  string `json:"domain"`
		Confirm string `json:"confirm"`
	}
	r.Register(actions.Action{Name: "mail.domain_add", Mutating: true, Description: "Host email for a domain and create its DKIM key; returns the DNS records to publish.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in domainReq
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		in.Domain = strings.ToLower(strings.TrimSpace(in.Domain))
		if !ValidDomain(in.Domain) {
			return nil, actions.InvalidParams("domain is not a valid domain name")
		}
		mu.Lock()
		defer mu.Unlock()
		if !e.configured() {
			return nil, actions.Conflict("Set up email first (Server → Email).")
		}
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		if _, ok := d.Domains[in.Domain]; ok {
			return map[string]any{"domain": in.Domain, "dkim_txt": d.Domains[in.Domain].DKIMPublic, "dkim_selector": d.Domains[in.Domain].Selector}, nil
		}
		priv, txt, err := newDKIM()
		if err != nil {
			return nil, actions.Failed("Could not create the DKIM key.", err.Error())
		}
		sel := "whacp" + e.Now().Format("200601")
		d.Domains[in.Domain] = Domain{Selector: sel, DKIMPublic: txt}
		if err := e.saveData(ctx, d, func(set *changeset.Set) {
			set.Write(dir+"/dkim/"+in.Domain+".private", priv, 0o600, e.owner("opendkim"))
		}); err != nil {
			return nil, err
		}
		return map[string]any{"domain": in.Domain, "dkim_selector": sel, "dkim_txt": txt}, nil
	}})

	r.Register(actions.Action{Name: "mail.domain_remove", Mutating: true, Description: "Stop hosting email for a domain (confirm = domain; its mailboxes and aliases must be removed first).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in domainReq
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		if _, ok := d.Domains[in.Domain]; !ok {
			return nil, actions.NotFound("email is not hosted for " + in.Domain)
		}
		if in.Confirm != in.Domain {
			return nil, actions.InvalidParams("confirm must repeat the domain")
		}
		for addr := range d.Mailboxes {
			if strings.HasSuffix(addr, "@"+in.Domain) {
				return nil, actions.Conflict("Remove the domain's mailboxes first.")
			}
		}
		for src := range d.Aliases {
			if strings.HasSuffix(src, "@"+in.Domain) {
				return nil, actions.Conflict("Remove the domain's aliases first.")
			}
		}
		delete(d.Domains, in.Domain)
		d.Suspended = slices.DeleteFunc(d.Suspended, func(s string) bool { return s == in.Domain })
		if err := e.saveData(ctx, d, func(set *changeset.Set) {
			if he.Root.Exists(dir + "/dkim/" + in.Domain + ".private") {
				set.Remove(dir + "/dkim/" + in.Domain + ".private")
			}
		}); err != nil {
			return nil, err
		}
		return map[string]any{"domain": in.Domain, "removed": true}, nil
	}})

	r.Register(actions.Action{Name: "mail.suspend", Mutating: true, Description: "Suspend or resume logins for a mail domain's mailboxes (account suspension); incoming mail is still delivered.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Domain    string `json:"domain"`
			Suspended bool   `json:"suspended"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		dom := strings.ToLower(in.Domain)
		if !ValidDomain(dom) {
			return nil, actions.InvalidParams("domain is not a valid domain name")
		}
		mu.Lock()
		defer mu.Unlock()
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		if _, ok := d.Domains[dom]; !ok {
			return map[string]any{"domain": dom, "suspended": in.Suspended, "hosted": false}, nil
		}
		was := slices.Contains(d.Suspended, dom)
		if was == in.Suspended {
			return map[string]any{"domain": dom, "suspended": in.Suspended, "hosted": true}, nil
		}
		d.Suspended = slices.DeleteFunc(d.Suspended, func(s string) bool { return s == dom })
		if in.Suspended {
			d.Suspended = append(d.Suspended, dom)
			slices.Sort(d.Suspended)
		}
		if err := e.saveData(ctx, d, nil); err != nil {
			return nil, err
		}
		return map[string]any{"domain": dom, "suspended": in.Suspended, "hosted": true}, nil
	}})

	r.Register(actions.Action{Name: "mail.mailbox_save", Mutating: true, Description: "Create a mailbox or change its password or quota (the password is stored as a SHA-512 crypt hash).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Address  string `json:"address"`
			Password string `json:"password"`
			QuotaMB  int64  `json:"quota_mb"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		local, dom, err := SplitAddress(in.Address)
		if err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		addr := local + "@" + dom
		if in.QuotaMB < 0 || in.QuotaMB > 10<<20 {
			return nil, actions.InvalidParams("quota_mb must be 0 (none) to 10485760")
		}
		mu.Lock()
		defer mu.Unlock()
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		if _, ok := d.Domains[dom]; !ok {
			return nil, actions.Conflict("Email is not hosted for " + dom + ".")
		}
		if _, ok := d.Aliases[addr]; ok {
			return nil, actions.Conflict(addr + " is an alias; remove the alias first.")
		}
		m, exists := d.Mailboxes[addr]
		if in.Password != "" {
			if len(in.Password) < 10 || len(in.Password) > 128 || strings.ContainsAny(in.Password, "\x00\r\n:") {
				return nil, actions.InvalidParams("password must be 10–128 characters (no colon)")
			}
			m.Hash = shacrypt.HashPassword(in.Password)
		} else if !exists {
			return nil, actions.InvalidParams("password is required for a new mailbox")
		}
		m.QuotaMB = in.QuotaMB
		d.Mailboxes[addr] = m
		if err := e.saveData(ctx, d, nil); err != nil {
			return nil, err
		}
		return map[string]any{"address": addr, "created": !exists, "quota_mb": m.QuotaMB}, nil
	}})

	r.Register(actions.Action{Name: "mail.mailbox_delete", Mutating: true, Description: "Delete a mailbox (confirm = address); its mail is moved to .whacp-trash in the mail folder, or kept in place with keep_mail.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Address  string `json:"address"`
			Confirm  string `json:"confirm"`
			KeepMail bool   `json:"keep_mail"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		local, dom, err := SplitAddress(in.Address)
		if err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		addr := local + "@" + dom
		if in.Confirm != addr {
			return nil, actions.InvalidParams("confirm must repeat the address")
		}
		mu.Lock()
		defer mu.Unlock()
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		if _, ok := d.Mailboxes[addr]; !ok {
			return nil, actions.NotFound("no mailbox " + addr)
		}
		delete(d.Mailboxes, addr)
		if err := e.saveData(ctx, d, nil); err != nil {
			return nil, err
		}
		res := map[string]any{"address": addr, "deleted": true}
		maildir := filepath.Join(e.M.VmailDir, dom, local)
		if !in.KeepMail && he.Root.Exists(maildir) {
			// The mailbox is gone from Dovecot either way; its mail must not
			// silently stay behind if it cannot be moved away.
			dest, err := he.MoveToTrash(maildir, e.M.VmailDir, "mail-"+addr)
			if err != nil {
				he.Log.Warn("mailbox mail not moved to trash", "address", addr, "error", err)
				res["notes"] = []string{"The mailbox was deleted but its mail could not be moved to the trash and is still in " + maildir + ": " + logging.Redact(err.Error())}
			} else {
				res["trash"] = dest
			}
		}
		return res, nil
	}})

	r.Register(actions.Action{Name: "mail.alias_save", Mutating: true, Description: "Create or change an alias (forwarder): source address, or @domain for a catch-all, to up to 50 destinations.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Source       string   `json:"source"`
			Destinations []string `json:"destinations"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		src := strings.ToLower(strings.TrimSpace(in.Source))
		dom := strings.TrimPrefix(src, "@")
		if !strings.HasPrefix(src, "@") {
			local, d2, err := SplitAddress(src)
			if err != nil {
				return nil, actions.InvalidParams(err.Error())
			}
			src, dom = local+"@"+d2, d2
		} else if !ValidDomain(dom) {
			return nil, actions.InvalidParams("source must be an address or @domain")
		}
		if len(in.Destinations) == 0 || len(in.Destinations) > 50 {
			return nil, actions.InvalidParams("give 1–50 destinations")
		}
		var dests []string
		seen := map[string]bool{}
		for _, dst := range in.Destinations {
			dst = strings.ToLower(strings.TrimSpace(dst))
			if !validDestination(dst) {
				return nil, actions.InvalidParams(fmt.Sprintf("%q is not a valid destination address", dst))
			}
			if dst == src {
				return nil, actions.InvalidParams("an alias cannot point to itself")
			}
			if !seen[dst] {
				seen[dst] = true
				dests = append(dests, dst)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		if _, ok := d.Domains[dom]; !ok {
			return nil, actions.Conflict("Email is not hosted for " + dom + ".")
		}
		if _, ok := d.Mailboxes[src]; ok {
			return nil, actions.Conflict(src + " is a mailbox; an alias cannot have the same address.")
		}
		d.Aliases[src] = dests
		if err := e.saveData(ctx, d, nil); err != nil {
			return nil, err
		}
		return map[string]any{"source": src, "destinations": dests}, nil
	}})

	r.Register(actions.Action{Name: "mail.alias_delete", Mutating: true, Description: "Delete an alias.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Source string `json:"source"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		src := strings.ToLower(strings.TrimSpace(in.Source))
		mu.Lock()
		defer mu.Unlock()
		d, err := e.load()
		if err != nil {
			return nil, actions.Failed("The mail data could not be read.", err.Error())
		}
		if _, ok := d.Aliases[src]; !ok {
			return nil, actions.NotFound("no alias " + src)
		}
		delete(d.Aliases, src)
		if err := e.saveData(ctx, d, nil); err != nil {
			return nil, err
		}
		return map[string]any{"source": src, "deleted": true}, nil
	}})
}

// Configured reports whether email was set up (Email → Set up and start).
func Configured(h *host.Env) bool { return env{h}.configured() }
