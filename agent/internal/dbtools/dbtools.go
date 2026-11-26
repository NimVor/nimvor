// Package dbtools installs phpMyAdmin and Adminer for the administrator, as database managers next to NimVor's own
// database browser. They are served on the panel's own HTTPS address under /dbtools/<tool>/ by a snippet that the
// panel's Nginx site includes, run in a PHP-FPM pool of their own under an account of their own (not the panel's,
// not a website's), and live in a read-only folder. NimVor never edits their files after installing them.
package dbtools

import (
	"context"
	cryptotls "crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/apps"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/phpmgr"
)

const (
	// Dir holds the tools (www/dbtools/<tool>), the pool's temporary files and the state; it is readable by the
	// web server and PHP-FPM and writable by neither (except tmp, by the pool's account).
	Dir      = host.DBToolsDir
	account  = "nimvor-dbtools"
	poolName = "nimvor-dbtools"
	header   = "Managed by NimVor (database tools). Do not edit: changes are overwritten."
	// includeLine makes the panel's Nginx site read the snippet; a glob, so Nginx starts without any tool.
	includeLine = "include /etc/nginx/nimvor-dbtools.d/*.conf;"
	snippetDir  = host.DBToolsNginxDir
	panelSite   = "nimvor-panel.conf"
)

// phpModules are what phpMyAdmin and Adminer need from PHP to work with MySQL and MariaDB.
var phpModules = []string{"mysqli", "mbstring", "xml", "session", "json", "ctype", "curl"}

// Tool is one database manager NimVor can install.
type Tool struct {
	ID          string
	Name        string
	Description string
	Def         apps.AppDef
}

// Tools are the managers, in the order shown.
var Tools = []Tool{
	{
		ID: "phpmyadmin", Name: "phpMyAdmin", Description: "The classic web interface for MySQL and MariaDB.",
		Def: apps.AppDef{
			Name: "phpMyAdmin", URL: "https://www.phpmyadmin.net/downloads/phpMyAdmin-latest-all-languages.tar.gz",
			SumURL: "https://www.phpmyadmin.net/downloads/phpMyAdmin-latest-all-languages.tar.gz.sha256", SumAlgo: "sha256",
			Format: "targz", Strip: 1,
			Files: map[string]string{
				"tmp/.keep": "",
				"config.inc.php": "<?php\n$cfg['blowfish_secret'] = '{{secret32}}';\n$i = 0;\n$i++;\n" +
					"$cfg['Servers'][$i]['auth_type'] = 'cookie';\n$cfg['Servers'][$i]['host'] = 'localhost';\n" +
					"$cfg['Servers'][$i]['compress'] = false;\n$cfg['Servers'][$i]['AllowNoPassword'] = false;\n" +
					"$cfg['TempDir'] = '" + Dir + "/tmp/phpmyadmin';\n$cfg['AllowArbitraryServer'] = false;\n",
			},
		},
	},
	{
		ID: "adminer", Name: "Adminer", Description: "Database manager in a single small PHP file.",
		Def: apps.AppDef{
			Name: "Adminer", GitHub: "vrana/adminer", AssetRe: `^adminer-[0-9.]+\.php$`, Format: "file", File: "adminer.php",
			Files: map[string]string{"index.php": adminerEntry},
		},
	},
}

// adminerEntry is Adminer's front page: the stock file with the server fixed to this machine, so Adminer cannot be
// used to reach other hosts.
const adminerEntry = `<?php
function adminer_object() {
    if (class_exists('Adminer\Adminer')) {
        class NimvorAdminer extends Adminer\Adminer {
            function credentials() { return ['localhost', $_GET['username'] ?? '', \Adminer\get_password()]; }
            function loginFormField($name, $heading, $value) { return $name === 'driver' || $name === 'server' ? '' : parent::loginFormField($name, $heading, $value); }
        }
    } else {
        class NimvorAdminer extends Adminer {
            function credentials() { return ['localhost', $_GET['username'] ?? '', get_password()]; }
            function loginFormField($name, $heading, $value) { return $name === 'driver' || $name === 'server' ? '' : parent::loginFormField($name, $heading, $value); }
        }
    }
    return new NimvorAdminer;
}
include './adminer.php';
`

func toolByID(id string) (Tool, bool) {
	for _, t := range Tools {
		if t.ID == id {
			return t, true
		}
	}
	return Tool{}, false
}

// State is dbtools.json.
type State struct {
	PHP   string               `json:"php"`
	Tools map[string]Installed `json:"tools"`
}

// Installed is one installed tool.
type Installed struct {
	InstalledAt string `json:"installed_at"`
}

type env struct{ *host.Env }

func (e env) statePath() string { return Dir + "/dbtools.json" }
func (e env) wwwDir() string    { return Dir + "/www" }
func (e env) toolDir(id string) string {
	return e.wwwDir() + "/dbtools/" + id
}
func (e env) socket() string           { return filepath.Join(e.M.PHPSocketDir, poolName+".sock") }
func (e env) poolFile(v string) string { return filepath.Join(e.M.PoolDir(v), poolName+".conf") }
func (e env) siteFile() string         { return filepath.Join(e.M.NginxAvailable, panelSite) }
func (e env) snippetFile() string      { return snippetDir + "/dbtools.conf" }

func (e env) load() State {
	s := State{Tools: map[string]Installed{}}
	if b, err := e.Root.ReadFile(e.statePath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.Tools == nil {
		s.Tools = map[string]Installed{}
	}
	return s
}

func (e env) save(set interface {
	Write(string, []byte, fs.FileMode, agentfs.Owner)
}, s State) {
	b, _ := json.MarshalIndent(s, "", "  ")
	set.Write(e.statePath(), b, 0o600, agentfs.RootOwner)
}

// fpmVersions lists installed PHP-FPM versions, newest first.
func (e env) fpmVersions() []string {
	var vs []string
	for _, v := range e.Discovery.PHP().Versions {
		if v.FPMBinary != "" {
			vs = append(vs, v.Version)
		}
	}
	sort.Slice(vs, func(i, j int) bool { return phpmgr.VersionLess(vs[j], vs[i]) })
	return vs
}

// phpFor picks the newest installed PHP-FPM version that has the extensions the tools need.
func (e env) phpFor(ctx context.Context) (string, error) {
	cands := e.fpmVersions()
	if len(cands) == 0 {
		return "", actions.Conflict("No PHP-FPM version is installed. Install a PHP version first (PHP in the menu).")
	}
	var problems []string
	for _, v := range cands {
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
		for _, m := range phpModules {
			if !have[m] && !(m == "xml" && have["dom"]) {
				missing = append(missing, m)
			}
		}
		if len(missing) == 0 {
			return v, nil
		}
		problems = append(problems, "PHP "+v+" lacks "+strings.Join(missing, ", "))
	}
	return "", actions.Conflict("phpMyAdmin and Adminer need PHP with " + strings.Join(phpModules, ", ") + ": " + strings.Join(problems, "; ") + ". Install the missing PHP extensions first.")
}

// renderPool is the PHP-FPM pool the tools run in.
func (e env) renderPool() string {
	basedir := strings.Join([]string{e.wwwDir() + "/", Dir + "/tmp/", "/usr/share/php/", "/dev/urandom", "/etc/phpmyadmin/"}, ":")
	return "; " + header + "\n\n[" + poolName + "]\n" +
		"user = " + account + "\ngroup = " + account + "\n" +
		"listen = " + e.socket() + "\nlisten.owner = " + e.M.WebUser + "\nlisten.group = " + e.M.WebUser + "\nlisten.mode = 0660\n" +
		"pm = ondemand\npm.max_children = 8\npm.process_idle_timeout = 10s\npm.max_requests = 500\nchdir = /\ncatch_workers_output = yes\n" +
		"php_admin_value[open_basedir] = " + basedir + "\n" +
		"php_admin_value[upload_tmp_dir] = " + Dir + "/tmp\nphp_admin_value[session.save_path] = " + Dir + "/tmp/sessions\nphp_admin_value[sys_temp_dir] = " + Dir + "/tmp\n" +
		"php_admin_value[error_log] = " + Dir + "/tmp/php-error.log\nphp_admin_flag[log_errors] = on\nphp_admin_flag[display_errors] = off\n" +
		"php_admin_value[memory_limit] = 512M\nphp_admin_value[upload_max_filesize] = 100M\nphp_admin_value[post_max_size] = 110M\nphp_admin_value[max_execution_time] = 300\n" +
		"php_admin_value[disable_functions] = exec,passthru,shell_exec,system,proc_open,popen,pcntl_exec\n"
}

// renderSnippet is the Nginx part the panel's site includes: one location for all installed tools. Only the
// front PHP files of a tool run (never the ones in its subfolders), and files of the tools that hold settings,
// libraries or temporary data are not served.
func (e env) renderSnippet(ids []string) string {
	alt := strings.Join(ids, "|")
	var b strings.Builder
	b.WriteString("# " + header + "\n")
	b.WriteString("location ^~ /dbtools/ {\n    root " + e.wwwDir() + ";\n    index index.php;\n    absolute_redirect off;\n    client_max_body_size 110m;\n")
	b.WriteString("    add_header X-Content-Type-Options \"nosniff\" always;\n    add_header X-Frame-Options \"SAMEORIGIN\" always;\n    add_header Referrer-Policy \"same-origin\" always;\n")
	fmt.Fprintf(&b, "    location ~ ^/dbtools/(%s)/[A-Za-z0-9_]+\\.php$ {\n        include fastcgi_params;\n        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;\n        fastcgi_param HTTPS on;\n        fastcgi_pass unix:%s;\n        fastcgi_read_timeout 300s;\n    }\n", alt, e.socket())
	b.WriteString("    location ~* \\.php$ {\n        return 404;\n    }\n")
	b.WriteString("    location ~ /\\. {\n        deny all;\n    }\n")
	b.WriteString("    location ~ ^/dbtools/[a-z]+/(libraries|setup|vendor|templates|sql|tmp|config|doc|examples)/ {\n        deny all;\n    }\n")
	b.WriteString("    location ~* \\.(inc|sql|sh|dist|lock|log|md|json|yml|xml)$ {\n        deny all;\n    }\n")
	b.WriteString("}\n")
	return b.String()
}

// withInclude returns the panel site's text with the include line added after its first "server_name" line, and
// whether it changed. A site that already has the line is left alone.
func withInclude(site string) (string, bool, error) {
	if strings.Contains(site, includeLine) {
		return site, false, nil
	}
	lines := strings.Split(site, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "client_max_body_size") {
			out := append([]string{}, lines[:i+1]...)
			out = append(out, "    "+includeLine)
			out = append(out, lines[i+1:]...)
			return strings.Join(out, "\n"), true, nil
		}
	}
	return "", false, fmt.Errorf("the panel's Nginx site has no client_max_body_size line to add the include after")
}

// ensureAccount creates the pool's account.
func (e env) ensureAccount(ctx context.Context) (agentfs.Owner, int, error) {
	if _, err := e.LookupUser(account); err != nil {
		out, rerr := e.Runner.Run(ctx, "useradd", "--system", "--home-dir", Dir, "--no-create-home", "--shell", "/usr/sbin/nologin", "--user-group", "--comment", "NimVor database tools", "--", account)
		if rerr != nil {
			return agentfs.Owner{}, 0, actions.Failed("Could not create the database tools account.", host.Output(out))
		}
	}
	u, err := e.LookupUser(account)
	if err != nil {
		if e.Cfg.SimulateCommands {
			return agentfs.Owner{UID: 0, GID: 0}, 0, nil
		}
		return agentfs.Owner{}, 0, actions.Failed("The database tools account is missing.", err.Error())
	}
	return agentfs.Owner{UID: u.UID, GID: u.GID}, u.GID, nil
}

// openPerms makes a freshly unpacked tool readable by the web server and PHP-FPM and writable by nobody:
// folders 0755, files 0644, and the configuration (holding a secret) readable by the pool's account only.
func openPerms(root string, gid int) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link", p)
		}
		if err := os.Chown(p, 0, 0); err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(p, 0o755)
		}
		if filepath.Base(p) == "config.inc.php" {
			if err := os.Chown(p, 0, gid); err != nil {
				return err
			}
			return os.Chmod(p, 0o640)
		}
		return os.Chmod(p, 0o644)
	})
}

// probe loads a tool's front page through Nginx on the panel's address (tests replace it).
var probe = func(ctx context.Context, url string) error {
	// The request goes to this machine's own Nginx; its certificate is the panel's, which may not name 127.0.0.1.
	tr := &http.Transport{TLSClientConfig: &cryptotls.Config{InsecureSkipVerify: true}} //nolint:gosec // loopback check of our own site
	c := &http.Client{Transport: tr, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var last error
	for i := 0; i < 10; i++ {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		res, err := c.Do(req)
		if err == nil {
			io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
			res.Body.Close()
			if res.StatusCode == 200 || res.StatusCode == 302 || res.StatusCode == 303 {
				return nil
			}
			err = fmt.Errorf("answered HTTP %d", res.StatusCode)
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return last
}
