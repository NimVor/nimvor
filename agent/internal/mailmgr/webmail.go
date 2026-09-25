package mailmgr

import (
	"context"
	"crypto/rand"
	cryptotls "crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/phpmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/pkgs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/selinux"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// Webmail with Roundcube from the distribution's packages. WhaCP never
// edits the package's files: Roundcube reads its configuration folder
// from ROUNDCUBE_CONFIG_DIR, which WhaCP's own PHP-FPM pool points at
// WebmailDir. Roundcube runs as its own account (not the web server's and
// not a site's), keeps its SQLite database and temporary files in
// WebmailData, signs in to Dovecot (IMAP, 993) and sends through Postfix
// (submission, 587) on this machine with the user's own password, and is
// served by an Nginx virtual host of its own over HTTPS.

const (
	webmailUser   = "whacp-webmail"
	webmailPool   = "whacp-webmail"
	webmailHeader = "Managed by WhaCP (webmail). Do not edit: changes are overwritten."
)

// Webmail is WhaCP's webmail state (webmail.json).
type Webmail struct {
	Enabled  bool   `json:"enabled"`
	Hostname string `json:"hostname"`
	PHP      string `json:"php"`
}

// webmailModules are the PHP extensions Roundcube needs.
var webmailModules = []string{"dom", "intl", "mbstring", "openssl", "pdo_sqlite"}

// webmailPackages are installed by mail.webmail_install (Debian family).
// php-fpm keeps apt from choosing Apache to satisfy Roundcube's web
// server dependency.
var webmailPackages = []string{"roundcube-core", "roundcube-sqlite3", "roundcube-plugins", "php-fpm", "php-sqlite3", "php-intl", "php-mbstring", "php-xml"}

// webmailProbe fetches the login page through Nginx (tests replace it).
var webmailProbe = func(ctx context.Context, hostname string) error {
	tr := &http.Transport{
		// The request goes to this machine; the certificate may be
		// self-signed until one is issued for the host name.
		TLSClientConfig: &cryptotls.Config{ServerName: hostname, InsecureSkipVerify: true}, //nolint:gosec // loopback check of our own vhost
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, "127.0.0.1:443")
		},
	}
	c := &http.Client{Transport: tr, Timeout: 20 * time.Second}
	var last error
	for i := 0; i < 20; i++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+hostname+"/", nil)
		resp, err := c.Do(req)
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && strings.Contains(string(body), "rcmloginuser") {
				return nil
			}
			err = fmt.Errorf("HTTP %d without the sign-in form: %.300s", resp.StatusCode, body)
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return last
}

// roundcubeDefaults is the distribution's defaults.inc.php (Debian keeps it
// in /etc/roundcube, some RHEL packages in the program's config folder).
func (e env) roundcubeDefaults() string {
	for _, p := range []string{e.M.RoundcubeDefaults, e.M.RoundcubeDir + "/config/defaults.inc.php"} {
		if e.Root.Exists(p) {
			return p
		}
	}
	return e.M.RoundcubeDefaults
}

// roundcubeLayout describes where the installed Roundcube looks for its
// configuration (error details when it does not start).
func (e env) roundcubeLayout() string {
	out := e.Describe(e.M.RoundcubeDefaults, e.M.RoundcubeDir+"/config/defaults.inc.php", e.M.WebmailDir+"/defaults.inc.php", e.M.WebmailDir+"/config.inc.php")
	if b, err := e.Root.ReadFile(e.M.RoundcubeDir + "/program/include/iniset.php"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.Contains(l, "CONFIG_DIR") {
				out += "iniset.php: " + strings.TrimSpace(l) + "\n"
			}
		}
	}
	return out
}

func (e env) webmailPath() string { return e.M.WebmailDir + "/webmail.json" }

func (e env) loadWebmail() Webmail {
	var w Webmail
	if b, err := e.Root.ReadFile(e.webmailPath()); err == nil {
		_ = json.Unmarshal(b, &w)
	}
	return w
}

func (e env) roundcubeInstalled() bool {
	return e.Root.Exists(e.M.RoundcubePublic+"/index.php") && e.Root.Exists(e.M.RoundcubeDefaults) && e.Root.Exists(e.M.RoundcubeDir+"/bin/updatedb.sh")
}

func (e env) webmailVhost() string { return filepath.Join(e.M.NginxAvailable, "whacp-webmail.conf") }
func (e env) webmailLink() string  { return filepath.Join(e.M.NginxEnabled, "whacp-webmail.conf") }
func (e env) webmailSocket() string {
	return filepath.Join(e.M.PHPSocketDir, webmailPool+".sock")
}
func (e env) webmailPoolFile(v string) string {
	return filepath.Join(e.M.PoolDir(v), webmailPool+".conf")
}

// fpmVersions lists installed PHP-FPM versions, newest first.
func (e env) fpmVersions() []string {
	var vs []string
	for _, v := range e.Discovery.PHP().Versions {
		if v.FPMBinary != "" && validate.PHPVersion("v", v.Version) == nil {
			vs = append(vs, v.Version)
		}
	}
	sort.Slice(vs, func(i, j int) bool { return phpmgr.VersionLess(vs[j], vs[i]) })
	return vs
}

// webmailInstallPackages are the packages (this system's names) that
// install Roundcube: Debian's packages, or on RHEL EPEL's roundcubemail
// with the extensions for Remi's PHP (the newest installed, else 8.3).
func (e env) webmailInstallPackages() []string {
	if e.Platform.PackageManager() != "dnf" {
		return webmailPackages
	}
	v := "8.3"
	if vs := e.fpmVersions(); len(vs) > 0 {
		v = vs[0]
	}
	return append([]string{"roundcubemail"}, e.Platform.PHPPackages(v, []string{"pdo", "intl", "mbstring", "xml"})...)
}

// webmailPHP picks the PHP version to run Roundcube: the requested one,
// else the newest installed FPM version with Roundcube's extensions.
func (e env) webmailPHP(ctx context.Context, want string) (string, error) {
	candidates := e.fpmVersions()
	if want != "" {
		if err := validate.PHPVersion("php_version", want); err != nil {
			return "", host.Invalid(err)
		}
		candidates = []string{want}
	}
	if len(candidates) == 0 {
		return "", actions.Conflict("No PHP-FPM version is installed. Install webmail (it installs PHP-FPM) or a PHP version first.")
	}
	var problems []string
	for _, v := range candidates {
		out, err := e.Runner.Run(ctx, "php"+v, "-m")
		if err != nil {
			problems = append(problems, "PHP "+v+" is not installed")
			continue
		}
		have := map[string]bool{}
		for _, m := range phpmgr.ParseModules(string(out)) {
			have[strings.ToLower(m)] = true
		}
		var missing []string
		for _, m := range webmailModules {
			if !have[m] {
				missing = append(missing, m)
			}
		}
		if len(missing) == 0 {
			return v, nil
		}
		problems = append(problems, "PHP "+v+" lacks "+strings.Join(missing, ", "))
	}
	return "", actions.Conflict("Roundcube needs PHP with " + strings.Join(webmailModules, ", ") + ": " + strings.Join(problems, "; ") + ". Install the missing PHP extensions first.")
}

var desKeyRe = regexp.MustCompile(`\$config\['des_key'\] = '([A-Za-z0-9]{24})';`)

// webmailKey keeps Roundcube's encryption key across changes (it
// encrypts the password kept in each session).
func (e env) webmailKey() (string, error) {
	if b, err := e.Root.ReadFile(e.M.WebmailDir + "/config.inc.php"); err == nil {
		if m := desKeyRe.FindSubmatch(b); m != nil {
			return string(m[1]), nil
		}
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	key := make([]byte, 24)
	for i := range key {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		key[i] = alphabet[n.Int64()]
	}
	return string(key), nil
}

// renderRoundcube is WhaCP's config.inc.php.
func renderRoundcube(data, desKey string) string {
	return `<?php
// ` + webmailHeader + `
$config = [];
$config['db_dsnw'] = 'sqlite:///` + data + `/roundcube.db?mode=0640';
$config['log_driver'] = 'file';
$config['log_dir'] = '` + data + `/logs/';
$config['temp_dir'] = '` + data + `/temp/';
// Dovecot and Postfix on this machine, signed in with the user's own
// address and password. The connection never leaves the machine, so the
// certificate (issued for the mail host name, or self-signed) is not
// checked against 127.0.0.1.
$config['imap_host'] = 'ssl://127.0.0.1:993';
$config['imap_conn_options'] = ['ssl' => ['verify_peer' => false, 'verify_peer_name' => false]];
$config['smtp_host'] = 'tls://127.0.0.1:587';
$config['smtp_user'] = '%u';
$config['smtp_pass'] = '%p';
$config['smtp_conn_options'] = ['ssl' => ['verify_peer' => false, 'verify_peer_name' => false]];
$config['des_key'] = '` + desKey + `';
$config['product_name'] = 'Webmail';
$config['support_url'] = '';
$config['skin'] = 'elastic';
$config['use_https'] = true;
$config['enable_installer'] = false;
$config['login_rate_limit'] = 3;
$config['max_message_size'] = '25M';
$config['plugins'] = ['archive', 'zipdownload'];
$config['drafts_mbox'] = 'Drafts';
$config['junk_mbox'] = 'Junk';
$config['sent_mbox'] = 'Sent';
$config['trash_mbox'] = 'Trash';
`
}

// renderWebmailPool is the PHP-FPM pool Roundcube runs in.
func (e env) renderWebmailPool() string {
	d := e.M.WebmailData
	basedir := strings.Join([]string{
		filepath.Dir(e.M.RoundcubePublic) + "/", e.M.RoundcubeDir + "/", filepath.Dir(e.M.RoundcubeDefaults) + "/",
		e.M.WebmailDir + "/", d + "/", "/usr/share/php/", "/usr/share/pear/", "/dev/urandom",
	}, ":")
	return "; " + webmailHeader + "\n\n[" + webmailPool + "]\n" +
		"user = " + webmailUser + "\ngroup = " + webmailUser + "\n" +
		"listen = " + e.webmailSocket() + "\nlisten.owner = " + e.M.WebUser + "\nlisten.group = " + e.M.WebUser + "\nlisten.mode = 0660\n" +
		"pm = ondemand\npm.max_children = 10\npm.process_idle_timeout = 10s\npm.max_requests = 500\nchdir = /\ncatch_workers_output = yes\n" +
		// Roundcube 1.5 appends file names to it without a separator.
		"env[ROUNDCUBE_CONFIG_DIR] = " + e.M.WebmailDir + "/\n" +
		// Roundcube's libraries come from the distribution (PEAR and others).
		"php_value[include_path] = .:/usr/share/php:/usr/share/pear\n" +
		"php_admin_value[open_basedir] = " + basedir + "\n" +
		"php_admin_value[upload_tmp_dir] = " + d + "/temp\nphp_admin_value[session.save_path] = " + d + "/temp\nphp_admin_value[sys_temp_dir] = " + d + "/temp\n" +
		"php_admin_value[error_log] = " + d + "/logs/php-error.log\nphp_admin_flag[log_errors] = on\nphp_admin_flag[display_errors] = off\n" +
		"php_admin_value[memory_limit] = 256M\nphp_admin_value[upload_max_filesize] = 25M\nphp_admin_value[post_max_size] = 30M\nphp_admin_value[max_execution_time] = 120\n" +
		"php_admin_value[disable_functions] = exec,passthru,shell_exec,system,proc_open,popen,pcntl_exec\n"
}

type vhostParams struct {
	Hostname, Cert, Key string
	HTTP2Direct, IPv6   bool
}

// renderWebmailVhost serves Roundcube's public folder. Only index.php
// runs; other PHP files and files that may hold settings are refused.
func (e env) renderWebmailVhost(p vhostParams) string {
	var b strings.Builder
	listen := func(port string) {
		fmt.Fprintf(&b, "    listen %s;\n", port)
		if p.IPv6 {
			fmt.Fprintf(&b, "    listen [::]:%s;\n", port)
		}
	}
	acme := "    location ^~ /.well-known/acme-challenge/ {\n        root " + e.M.ACMERoot + ";\n        default_type \"text/plain\";\n        try_files $uri =404;\n    }\n"
	b.WriteString("# " + webmailHeader + "\n\nserver {\n")
	listen("80")
	fmt.Fprintf(&b, "    server_name %s;\n%s    location / {\n        return 301 https://$host$request_uri;\n    }\n}\n\nserver {\n", p.Hostname, acme)
	if p.HTTP2Direct {
		listen("443 ssl")
		b.WriteString("    http2 on;\n")
	} else {
		listen("443 ssl http2")
	}
	fmt.Fprintf(&b, "    server_name %s;\n    ssl_certificate %s;\n    ssl_certificate_key %s;\n", p.Hostname, p.Cert, p.Key)
	b.WriteString("    ssl_protocols TLSv1.2 TLSv1.3;\n    ssl_prefer_server_ciphers off;\n    ssl_session_cache shared:WhaCPSSL:10m;\n    ssl_session_timeout 1d;\n")
	b.WriteString("    add_header Strict-Transport-Security \"max-age=31536000\" always;\n    add_header X-Content-Type-Options \"nosniff\" always;\n    add_header Referrer-Policy \"same-origin\" always;\n")
	fmt.Fprintf(&b, "    root %s;\n    index index.php;\n    client_max_body_size 30m;\n", e.M.RoundcubePublic)
	b.WriteString("    access_log /var/log/nginx/whacp-webmail.access.log;\n    error_log /var/log/nginx/whacp-webmail.error.log warn;\n")
	b.WriteString(acme)
	b.WriteString("    location ~ /\\.(?!well-known) {\n        deny all;\n    }\n")
	b.WriteString("    location ~ (^|/)(README|CHANGELOG|INSTALL|UPGRADING|LICENSE|composer\\.|meta\\.json|jsdeps\\.json) {\n        deny all;\n    }\n")
	b.WriteString("    location ~ \\.(inc|sql|sh|dist|lock|log|md|txt|php)$ {\n        deny all;\n    }\n")
	fmt.Fprintf(&b, "    location = /index.php {\n        include fastcgi_params;\n        fastcgi_param SCRIPT_FILENAME $document_root/index.php;\n        fastcgi_param HTTPS on;\n        fastcgi_pass unix:%s;\n        fastcgi_read_timeout 130s;\n    }\n", e.webmailSocket())
	b.WriteString("    location / {\n        try_files $uri $uri/ =404;\n    }\n}\n")
	return b.String()
}

// webmailStatus is reported by mail.status.
func (e env) webmailStatus(ctx context.Context) map[string]any {
	w := e.loadWebmail()
	running := false
	if w.Enabled && w.PHP != "" {
		running = e.UnitActive(ctx, e.M.FPMUnit(w.PHP)) && e.Root.Exists(e.webmailVhost())
	}
	return map[string]any{"installed": e.roundcubeInstalled(), "enabled": w.Enabled, "hostname": w.Hostname, "php": w.PHP, "running": running}
}

// registerWebmail adds mail.webmail_install and mail.webmail_configure.
func registerWebmail(r *actions.Registry, e env) {
	r.Register(actions.Action{Name: "mail.webmail_install", Mutating: true, Description: "Install Roundcube webmail from the distribution's packages (with PHP-FPM and SQLite support; the package's own database setup is skipped because WhaCP keeps Roundcube's data itself). Runs as a job.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := host.Decode(raw, &struct{}{}); err != nil {
			return nil, err
		}
		if !pkgs.Available(e.Env) {
			return nil, actions.Conflict("The package manager is not available on this system; install Roundcube yourself.")
		}
		packages := e.webmailInstallPackages()
		j := e.Jobs.Start("mail.webmail_install", "mail:webmail", func(ctx context.Context, log io.Writer) (any, error) {
			if e.Platform.PackageManager() == "apt" {
				// Roundcube's package would otherwise ask (or default) to set
				// up a MySQL database of its own.
				if _, err := e.Runner.RunWith(ctx, "debconf-set-selections", execx.Opts{Stdin: strings.NewReader("roundcube-core roundcube/dbconfig-install boolean false\n"), Output: log}); err != nil {
					return nil, errors.New("could not preset the Roundcube package's questions")
				}
			}
			return map[string]any{"packages": packages}, pkgs.InstallNative(ctx, e.Env, log, packages)
		})
		return map[string]any{"job_id": j.ID, "packages": packages}, nil
	}})

	r.Register(actions.Action{Name: "mail.webmail_configure", Mutating: true, Description: "Turn webmail (Roundcube) on for a host name, or off. WhaCP writes Roundcube's configuration in its own folder, runs it in its own PHP-FPM pool and account, creates or upgrades its SQLite database and serves it with its own Nginx site over HTTPS (Let's Encrypt certificate for the host name when present, otherwise self-signed). Validated with nginx -t and php-fpm -t, checked by loading the sign-in page, rolled back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		in := struct {
			Enabled    bool   `json:"enabled"`
			Hostname   string `json:"hostname"`
			PHPVersion string `json:"php_version"`
		}{}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		prev := e.loadWebmail()
		if !in.Enabled {
			return e.disableWebmail(ctx, prev)
		}
		in.Hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Hostname), "."))
		if !hostnameRe.MatchString(in.Hostname) {
			return nil, actions.InvalidParams("hostname must be a full host name for webmail, e.g. webmail.example.com or the mail host name")
		}
		if !e.configured() {
			return nil, actions.Conflict("Set up email first.")
		}
		if !e.roundcubeInstalled() {
			return nil, actions.Conflict("Roundcube is not installed. Install webmail first.")
		}
		for _, w := range e.Discovery.Websites(ctx).Websites {
			mine := false
			for _, f := range w.ConfigFiles {
				mine = mine || f == e.webmailVhost() || f == e.webmailLink()
			}
			if !mine && (w.Domain == in.Hostname || contains(w.Aliases, in.Hostname)) {
				return nil, actions.Conflict("The website " + w.Domain + " already answers for " + in.Hostname + ". Choose another host name for webmail.")
			}
		}
		v, err := e.webmailPHP(ctx, in.PHPVersion)
		if err != nil {
			return nil, err
		}
		if !e.Root.IsDir(e.M.PoolDir(v)) && !e.Cfg.SimulateCommands {
			return nil, actions.Conflict("PHP " + v + " has no FPM pool folder (" + e.M.PoolDir(v) + ").")
		}
		if err := e.PrecheckNginx(ctx); err != nil {
			return nil, err
		}
		if _, err := e.LookupUser(webmailUser); err != nil {
			if err := e.run(ctx, "useradd", "--system", "--home-dir", e.M.WebmailData, "--no-create-home", "--shell", "/usr/sbin/nologin", "--user-group", "--comment", "WhaCP webmail", "--", webmailUser); err != nil {
				return nil, actions.Failed("Could not create the webmail account.", err.Error())
			}
		}
		acct, err := e.LookupUser(webmailUser)
		if err != nil && !e.Cfg.SimulateCommands {
			return nil, actions.Failed("The webmail account is missing.", err.Error())
		}
		own := agentfs.Owner{UID: acct.UID, GID: acct.GID}
		key, err := e.webmailKey()
		if err != nil {
			return nil, actions.Failed("Could not create the webmail encryption key.", err.Error())
		}
		cert, certKey := e.M.WebmailDir+"/tls.crt", e.M.WebmailDir+"/tls.key"
		le := "/etc/letsencrypt/live/" + in.Hostname
		if e.Root.Exists(le+"/fullchain.pem") && e.Root.Exists(le+"/privkey.pem") {
			cert, certKey = le+"/fullchain.pem", le+"/privkey.pem"
		}

		set := e.Changes("webmail")
		set.Mkdir(e.M.WebmailDir, 0o750, agentfs.Owner{UID: 0, GID: acct.GID})
		set.Mkdir(e.M.WebmailData, 0o750, own)
		set.Mkdir(e.M.WebmailData+"/temp", 0o750, own)
		set.Mkdir(e.M.WebmailData+"/logs", 0o750, own)
		set.Mkdir(e.M.ACMERoot, 0o755, agentfs.RootOwner)
		set.Write(e.M.WebmailDir+"/defaults.inc.php", []byte("<?php\n// "+webmailHeader+"\ninclude '"+e.roundcubeDefaults()+"';\n"), 0o644, agentfs.RootOwner)
		set.Write(e.M.WebmailDir+"/config.inc.php", []byte(renderRoundcube(e.M.WebmailData, key)), 0o640, agentfs.Owner{UID: 0, GID: acct.GID})
		if cert == e.M.WebmailDir+"/tls.crt" && !e.Root.Exists(cert) {
			c, k, err := selfSigned(in.Hostname)
			if err != nil {
				return nil, actions.Failed("Could not create the webmail certificate.", err.Error())
			}
			set.Write(cert, c, 0o644, agentfs.RootOwner)
			set.Write(certKey, k, 0o600, agentfs.RootOwner)
		}
		set.Write(e.webmailPoolFile(v), []byte(e.renderWebmailPool()), 0o644, agentfs.RootOwner)
		if e.M.PHPSocketTmpfiles != "" {
			set.Write(e.M.PHPSocketTmpfiles, []byte("# Managed by WhaCP: PHP-FPM sockets of websites.\nd "+e.M.PHPSocketDir+" 0755 root root -\n"), 0o644, agentfs.RootOwner)
			if !e.Root.Exists(e.M.PHPSocketDir) {
				set.Mkdir(e.M.PHPSocketDir, 0o755, agentfs.RootOwner)
			}
		}
		if !e.Root.Exists(e.M.NginxAvailable) {
			set.Mkdir(e.M.NginxAvailable, 0o755, agentfs.RootOwner)
		}
		if prev.PHP != "" && prev.PHP != v {
			set.Remove(e.webmailPoolFile(prev.PHP))
		}
		vh := vhostParams{Hostname: in.Hostname, Cert: cert, Key: certKey, HTTP2Direct: e.NginxAtLeast(ctx, 1, 25, 1), IPv6: e.IPv6()}
		set.Write(e.webmailVhost(), []byte(e.renderWebmailVhost(vh)), 0o644, agentfs.RootOwner)
		set.Link(e.webmailVhost(), e.webmailLink())
		state, _ := json.MarshalIndent(Webmail{Enabled: true, Hostname: in.Hostname, PHP: v}, "", "  ")
		set.Write(e.webmailPath(), state, 0o600, agentfs.RootOwner)

		applied, err := set.Commit(func() error {
			if err := e.FPMTest(ctx, v); err != nil {
				return err
			}
			return e.NginxTest(ctx)
		})
		if err != nil {
			return nil, host.FailWithValidation("Webmail was not set up", err)
		}
		reload := func() {
			e.ReloadFPM(ctx, v)
			if prev.PHP != "" && prev.PHP != v {
				e.ReloadFPM(ctx, prev.PHP)
			}
			e.ReloadNginx(ctx)
		}
		fail := func(msg string, cause error) (any, error) {
			applied.Rollback()
			reload()
			return nil, actions.Failed(msg+" The previous configuration was restored.", cause.Error())
		}
		// SELinux: PHP-FPM (as the web server) writes Roundcube's data and
		// connects to IMAP and submission.
		if err := selinux.Label(ctx, e.Env, e.M.WebmailData, "httpd_sys_rw_content_t"); err != nil {
			return fail("Roundcube's data folder could not be labelled for SELinux.", err)
		}
		if err := selinux.Boolean(ctx, e.Env, "httpd_can_network_connect"); err != nil {
			return fail("SELinux does not let the web server connect to the mail server.", err)
		}
		// Creates the database, or upgrades it after a Roundcube update;
		// run as the webmail account so it owns the file.
		out, err := e.Runner.RunWith(ctx, "php"+v, execx.Opts{
			As:  &execx.Credential{UID: uint32(acct.UID), GID: uint32(acct.GID), Home: e.M.WebmailData},
			Env: []string{"ROUNDCUBE_CONFIG_DIR=" + e.M.WebmailDir + "/"},
		}, "-d", "error_reporting=E_ALL & ~E_DEPRECATED", e.M.RoundcubeDir+"/bin/updatedb.sh", "--package=roundcube", "--dir="+e.M.RoundcubeDir+"/SQL")
		if err != nil {
			if strings.Contains(string(out), "Cannot redeclare function") {
				// e.g. Roundcube 1.6.11 with PHP 8.5, which added array_first().
				return fail("The installed Roundcube package does not work with PHP "+v+" yet. Install an update of the package when the distribution provides one, or choose another installed PHP version.", errors.New(host.Output(out)))
			}
			return fail("Roundcube's database could not be created or upgraded.", errors.New(host.Output(out)+"\n"+e.roundcubeLayout()))
		}
		if err := e.ReloadFPM(ctx, v); err != nil {
			return fail("PHP-FPM did not load the webmail pool.", err)
		}
		if prev.PHP != "" && prev.PHP != v {
			e.ReloadFPM(ctx, prev.PHP)
		}
		if err := e.ReloadNginx(ctx); err != nil {
			return fail("Nginx did not reload.", err)
		}
		if !e.Cfg.SimulateCommands && e.UnitActive(ctx, e.M.NginxUnit) {
			if err := webmailProbe(ctx, in.Hostname); err != nil {
				return fail("The webmail sign-in page did not load.", err)
			}
		}
		notes := []string{"Users sign in at https://" + in.Hostname + "/ with their full email address and mailbox password."}
		if cert == e.M.WebmailDir+"/tls.crt" {
			notes = append(notes, "A self-signed certificate is used; point "+in.Hostname+" at this server, issue a Let's Encrypt certificate for it and turn webmail on again to use it.")
		}
		return map[string]any{"enabled": true, "hostname": in.Hostname, "php": v, "url": "https://" + in.Hostname + "/", "certificate": cert, "notes": notes}, nil
	}})
}

// disableWebmail removes the site and the pool; the configuration and
// data (address books, settings) stay for when it is turned on again.
func (e env) disableWebmail(ctx context.Context, prev Webmail) (any, error) {
	if !prev.Enabled {
		return map[string]any{"enabled": false}, nil
	}
	set := e.Changes("webmail-off")
	set.Remove(e.webmailLink())
	set.Remove(e.webmailVhost())
	if prev.PHP != "" {
		set.Remove(e.webmailPoolFile(prev.PHP))
	}
	prev.Enabled = false
	state, _ := json.MarshalIndent(prev, "", "  ")
	set.Write(e.webmailPath(), state, 0o600, agentfs.RootOwner)
	applied, err := set.Commit(func() error { return e.NginxTest(ctx) })
	if err != nil {
		return nil, host.FailWithValidation("Webmail was not turned off", err)
	}
	if err := e.ReloadNginx(ctx); err != nil {
		applied.Rollback()
		e.ReloadNginx(ctx)
		return nil, actions.Failed("Nginx did not reload; webmail stays on.", err.Error())
	}
	if prev.PHP != "" {
		if err := e.ReloadFPM(ctx, prev.PHP); err != nil {
			e.Log.Warn("php-fpm reload after removing the webmail pool failed", "err", err)
		}
	}
	return map[string]any{"enabled": false}, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
