// Package wordpress is the WordPress toolkit: it finds WordPress
// installations in websites and manages them (overview, core, plugin and
// theme updates, plugins and themes, maintenance mode, a few hardening
// settings, core file verification and one-time administrator sign-in).
//
// Everything that touches a website runs as that website's account, in its
// slice, through PHP: finding installations only reads files (WordPress is
// not loaded); managing them uses WP-CLI (pinned, checksum-verified, kept
// root-owned) with the website's plugins and themes not loaded, so their
// code cannot run in the toolkit's commands. The agent never reads or
// returns database passwords.
package wordpress

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// Site is a website to look into.
type Site struct {
	Domain     string `json:"domain"`
	User       string `json:"user"`
	Root       string `json:"root"`
	PHPVersion string `json:"php_version"`
}

// Target is one installation: a folder of a website, relative to its root.
type Target struct {
	Site
	Path string `json:"path"`
}

func (s *Site) validate(env *host.Env) error {
	if err := validate.Domain("domain", s.Domain); err != nil {
		return err
	}
	if err := validate.Username("user", s.User); err != nil {
		return err
	}
	if err := validate.Within("root", env.Cfg.SitesRoot, s.Root); err != nil {
		return err
	}
	if filepath.Clean(s.Root) == filepath.Clean(env.Cfg.SitesRoot) {
		return &validate.Error{Field: "root", Message: "must be a website folder"}
	}
	return validate.PHPVersion("php_version", s.PHPVersion)
}

func (t *Target) validate(env *host.Env) error {
	if err := t.Site.validate(env); err != nil {
		return err
	}
	t.Path = strings.Trim(filepath.Clean("/"+t.Path), "/")
	if err := validate.RelPath("path", t.Path); err != nil {
		return err
	}
	switch strings.Split(t.Path, "/")[0] {
	case "logs", "tmp", "backups", ".ssh":
		return &validate.Error{Field: "path", Message: "is not a website folder"}
	}
	if strings.ContainsAny(t.Path, "\"'$`\\;") {
		return &validate.Error{Field: "path", Message: "contains characters that are not allowed"}
	}
	return nil
}

// dir is the installation's folder on this machine.
func (t Target) dir(env *host.Env) (string, error) {
	root, err := env.Root.Path(t.Root)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, t.Path), nil
}

// runner runs PHP as a website account.
type runner struct {
	env  *host.Env
	cred *execx.Credential
	php  string
	root string // the website folder on this machine
}

func newRunner(env *host.Env, s Site) (*runner, error) {
	cred, _, err := env.Credential(s.User)
	if err != nil {
		return nil, actions.InvalidParams("user: " + err.Error())
	}
	php := "php" + s.PHPVersion
	if !env.Runner.Available(php) {
		return nil, actions.Conflict("PHP " + s.PHPVersion + " is not installed; choose another PHP version for the website.")
	}
	root, err := env.Root.Path(s.Root)
	if err != nil {
		return nil, actions.InvalidParams("root: " + err.Error())
	}
	return &runner{env: env, cred: cred, php: php, root: root}, nil
}

// limited keeps at most n bytes.
type limited struct {
	bytes.Buffer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.n - l.Len(); room > 0 {
		if len(p) > room {
			l.Buffer.Write(p[:room])
		} else {
			l.Buffer.Write(p)
		}
	}
	return len(p), nil
}

// result reads the "WHACP-JSON" line a script printed into v.
func result(stdout []byte, v any) error {
	lines := strings.Split(string(stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if rest, ok := strings.CutPrefix(strings.TrimRight(lines[i], "\r"), marker); ok {
			return json.Unmarshal([]byte(rest), v)
		}
	}
	return fmt.Errorf("no result")
}

// script runs one of the agent's PHP programs as the account.
func (r *runner) script(ctx context.Context, code string, env []string, timeout time.Duration, args ...string) ([]byte, []byte, error) {
	out := &limited{n: 8 << 20}
	errOut, err := r.env.Runner.RunWith(ctx, r.php, execx.Opts{As: r.cred, Dir: "/", Env: env, Stdin: strings.NewReader(code), Stdout: out, Timeout: timeout}, args...)
	return out.Bytes(), errOut, err
}

// wp runs WP-CLI as the account for the installation in dir. Plugins and
// themes are not loaded; the global WP-CLI configuration is ignored.
func (r *runner) wp(ctx context.Context, dir string, o execx.Opts, args ...string) ([]byte, error) {
	o.As, o.Dir = r.cred, "/"
	if o.Timeout == 0 {
		o.Timeout = 2 * time.Minute
	}
	o.Env = append(o.Env,
		"WP_CLI_CACHE_DIR="+filepath.Join(r.root, "tmp", "wp-cli-cache"),
		"WP_CLI_CONFIG_PATH=/dev/null",
		"WP_CLI_DISABLE_AUTO_CHECK_UPDATE=1",
		"WP_CLI_STRICT_ARGS_MODE=1",
	)
	full := append([]string{"-d", "memory_limit=512M", "-d", "display_errors=stderr", WPCLIPath,
		"--path=" + dir, "--skip-plugins", "--skip-themes", "--no-color"}, args...)
	return r.env.Runner.RunWith(ctx, r.php, o, full...)
}

// cliError is WP-CLI's own message ("Error: …") or the output.
func cliError(out []byte) string {
	var last string
	for _, l := range strings.Split(string(out), "\n") {
		if m, ok := strings.CutPrefix(strings.TrimSpace(l), "Error: "); ok {
			last = m
		}
	}
	if last != "" {
		return last
	}
	return host.Output(out)
}

// Installation is one WordPress found in a website.
type Installation struct {
	Domain           string `json:"domain"`
	Path             string `json:"path"`
	Version          string `json:"version"`
	Configured       bool   `json:"configured"`
	DBName           string `json:"db_name"`
	DBHost           string `json:"db_host"`
	TablePrefix      string `json:"table_prefix"`
	Debug            bool   `json:"debug"`
	FileEditDisabled bool   `json:"file_edit_disabled"`
	ConfigMode       string `json:"config_mode"`
	Maintenance      bool   `json:"maintenance"`
}

var (
	versionRe = regexp.MustCompile(`^[0-9][0-9A-Za-z.+-]{0,30}$`)
	slugRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	modeRe    = regexp.MustCompile(`^0[0-7]{3}$`)
)

func clip(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// scanSite finds the installations of one website.
func scanSite(ctx context.Context, env *host.Env, s Site) ([]Installation, error) {
	r, err := newRunner(env, s)
	if err != nil {
		return nil, err
	}
	stdout, errOut, err := r.script(ctx, scanScript, []string{"WHACP_ROOT=" + r.root}, time.Minute)
	var res struct {
		Installs []struct {
			Dir              string `json:"dir"`
			Version          string `json:"version"`
			DBName           string `json:"db_name"`
			DBHost           string `json:"db_host"`
			TablePrefix      string `json:"table_prefix"`
			ConfigMode       string `json:"config_mode"`
			Configured       bool   `json:"configured"`
			Debug            bool   `json:"debug"`
			FileEditDisabled bool   `json:"file_edit_disabled"`
			Maintenance      bool   `json:"maintenance"`
		} `json:"installs"`
	}
	if err != nil || result(stdout, &res) != nil {
		return nil, fmt.Errorf("%s could not be scanned: %s", s.Domain, host.Why(errOut, err))
	}
	var out []Installation
	for _, i := range res.Installs {
		rel, err := filepath.Rel(r.root, filepath.Clean(i.Dir))
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			continue // not inside the website: ignored
		}
		if rel == "." {
			rel = ""
		}
		in := Installation{Domain: s.Domain, Path: clip(rel, 255), Configured: i.Configured, DBName: clip(i.DBName, 64), DBHost: clip(i.DBHost, 255),
			TablePrefix: clip(i.TablePrefix, 64), Debug: i.Debug, FileEditDisabled: i.FileEditDisabled, Maintenance: i.Maintenance}
		if versionRe.MatchString(i.Version) {
			in.Version = i.Version
		}
		if modeRe.MatchString(i.ConfigMode) {
			in.ConfigMode = i.ConfigMode
		}
		out = append(out, in)
	}
	return out, nil
}

// LatestVersion asks WordPress.org for the current WordPress release
// (tests replace it). Cached for six hours.
var LatestVersion = func(ctx context.Context) string {
	latestMu.Lock()
	defer latestMu.Unlock()
	if latestVal != "" && time.Since(latestAt) < 6*time.Hour {
		return latestVal
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.wordpress.org/core/version-check/1.7/", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return latestVal
	}
	defer res.Body.Close()
	var body struct {
		Offers []struct {
			Response string `json:"response"`
			Current  string `json:"current"`
		} `json:"offers"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body) != nil {
		return latestVal
	}
	for _, o := range body.Offers {
		if o.Response == "upgrade" && versionRe.MatchString(o.Current) {
			latestVal, latestAt = o.Current, time.Now()
			break
		}
	}
	return latestVal
}

var (
	latestMu  sync.Mutex
	latestVal string
	latestAt  time.Time
)

// Info is an installation's overview.
type Info struct {
	Version         string         `json:"version"`
	PHP             string         `json:"php"`
	MySQL           string         `json:"mysql"`
	Multisite       bool           `json:"multisite"`
	SiteURL         string         `json:"siteurl"`
	Home            string         `json:"home"`
	Title           string         `json:"title"`
	SearchEngines   bool           `json:"search_engines"`
	Core            CoreInfo       `json:"core"`
	Plugins         []Extension    `json:"plugins"`
	Themes          []Extension    `json:"themes"`
	Admins          []Admin        `json:"admins"`
	AdminLoginExist bool           `json:"admin_login_exists"`
	Constants       map[string]any `json:"constants"`
	Maintenance     bool           `json:"maintenance"`
	DB              DBInfo         `json:"db"`
	ConfigMode      string         `json:"config_mode"`
}

// CoreInfo is the newest WordPress this installation was offered.
type CoreInfo struct {
	Latest    string `json:"latest"`
	PHPNeeded string `json:"php_needed"`
	CheckedAt int64  `json:"checked_at"`
}

// Extension is a plugin or theme.
type Extension struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Version     string `json:"version"`
	Active      bool   `json:"active"`
	Update      string `json:"update"`
	AutoUpdate  bool   `json:"auto_update"`
	MustUse     bool   `json:"must_use,omitempty"`
	Parent      bool   `json:"parent,omitempty"`
	RequiresPHP string `json:"requires_php,omitempty"`
}

// Admin is an administrator account of the installation.
type Admin struct {
	ID    int    `json:"id"`
	Login string `json:"login"`
}

// DBInfo describes the database (never its password).
type DBInfo struct {
	Name   string `json:"name"`
	Host   string `json:"host"`
	Prefix string `json:"prefix"`
	Tables int    `json:"tables"`
	Bytes  int64  `json:"bytes"`
}

// sanitize bounds what the website's WordPress reported.
func (i *Info) sanitize() {
	i.Version, i.PHP, i.MySQL = clip(i.Version, 40), clip(i.PHP, 40), clip(i.MySQL, 80)
	i.SiteURL, i.Home, i.Title = clip(i.SiteURL, 500), clip(i.Home, 500), clip(i.Title, 200)
	i.Core.Latest, i.Core.PHPNeeded = clip(i.Core.Latest, 40), clip(i.Core.PHPNeeded, 40)
	fix := func(list []Extension) []Extension {
		if len(list) > 500 {
			list = list[:500]
		}
		out := list[:0]
		for _, e := range list {
			if !slugRe.MatchString(e.Name) {
				continue
			}
			e.Title, e.Version, e.Update, e.RequiresPHP = clip(e.Title, 200), clip(e.Version, 40), clip(e.Update, 40), clip(e.RequiresPHP, 20)
			out = append(out, e)
		}
		return out
	}
	i.Plugins, i.Themes = fix(i.Plugins), fix(i.Themes)
	if len(i.Admins) > 20 {
		i.Admins = i.Admins[:20]
	}
	for k := range i.Admins {
		i.Admins[k].Login = clip(i.Admins[k].Login, 60)
	}
	known := map[string]bool{"WP_DEBUG": true, "WP_DEBUG_DISPLAY": true, "DISALLOW_FILE_EDIT": true, "DISALLOW_FILE_MODS": true, "WP_AUTO_UPDATE_CORE": true, "AUTOMATIC_UPDATER_DISABLED": true, "DISABLE_WP_CRON": true}
	for k, v := range i.Constants {
		switch x := v.(type) {
		case bool, nil:
		case string:
			i.Constants[k] = clip(x, 20)
		default:
			delete(i.Constants, k)
		}
		if !known[k] {
			delete(i.Constants, k)
		}
	}
	i.DB.Name, i.DB.Host, i.DB.Prefix = clip(i.DB.Name, 64), clip(i.DB.Host, 255), clip(i.DB.Prefix, 64)
	if !modeRe.MatchString(i.ConfigMode) {
		i.ConfigMode = ""
	}
}

func info(ctx context.Context, env *host.Env, t Target, force bool) (*Info, error) {
	r, dir, err := prepare(ctx, env, t)
	if err != nil {
		return nil, err
	}
	env2 := []string{}
	if force {
		env2 = append(env2, "WHACP_FORCE=1")
	}
	stdout := &limited{n: 8 << 20}
	errOut, err := r.wp(ctx, dir, execx.Opts{Stdin: strings.NewReader(infoScript), Stdout: stdout, Env: env2}, "eval-file", "-")
	var i Info
	if err != nil || result(stdout.Bytes(), &i) != nil {
		return nil, actions.Failed("WordPress in "+display(t)+" could not be read: "+cliError(append(errOut, stdout.Bytes()...)), "")
	}
	i.sanitize()
	return &i, nil
}

func display(t Target) string {
	if t.Path == "" {
		return t.Domain
	}
	return t.Domain + " (" + t.Path + ")"
}

// prepare validates a target and makes WP-CLI available.
func prepare(ctx context.Context, env *host.Env, t Target) (*runner, string, error) {
	r, err := newRunner(env, t.Site)
	if err != nil {
		return nil, "", err
	}
	dir, err := t.dir(env)
	if err != nil {
		return nil, "", actions.InvalidParams("path: " + err.Error())
	}
	if !env.Root.Exists(filepath.Join(t.Root, t.Path, "wp-settings.php")) {
		return nil, "", actions.NotFound("There is no WordPress in " + display(t) + " any more; scan the website again.")
	}
	if err := ensurePhar(ctx, env, DefaultSource); err != nil {
		return nil, "", actions.Failed("WP-CLI, which manages WordPress, is not available: "+err.Error(), "")
	}
	return r, dir, nil
}

// Operation is a change to an installation.
type Operation struct {
	Target
	Op    string   `json:"op"`
	Items []string `json:"items"`
	Value string   `json:"value"`
	// Password is the new password of admin_password and db_password
	// (only ever sent to PHP on standard input).
	Password string `json:"password"`
}

// ops maps an operation to its WP-CLI arguments; items are appended where
// "items" says so (1: exactly one, n: one or more, all: none means --all).
var ops = map[string]struct {
	args  []string
	items string
	job   bool
}{
	"core_update":        {args: []string{"core", "update"}, job: true},
	"plugin_update":      {args: []string{"plugin", "update"}, items: "all", job: true},
	"theme_update":       {args: []string{"theme", "update"}, items: "all", job: true},
	"plugin_activate":    {args: []string{"plugin", "activate"}, items: "n"},
	"plugin_deactivate":  {args: []string{"plugin", "deactivate"}, items: "n"},
	"plugin_auto_on":     {args: []string{"plugin", "auto-updates", "enable"}, items: "n"},
	"plugin_auto_off":    {args: []string{"plugin", "auto-updates", "disable"}, items: "n"},
	"theme_activate":     {args: []string{"theme", "activate"}, items: "1"},
	"theme_auto_on":      {args: []string{"theme", "auto-updates", "enable"}, items: "n"},
	"theme_auto_off":     {args: []string{"theme", "auto-updates", "disable"}, items: "n"},
	"maintenance_on":     {args: []string{"maintenance-mode", "activate"}},
	"maintenance_off":    {args: []string{"maintenance-mode", "deactivate"}},
	"disable_file_edit":  {args: []string{"config", "set", "DISALLOW_FILE_EDIT", "true", "--raw", "--type=constant"}},
	"debug_off":          {args: []string{"config", "set", "WP_DEBUG", "false", "--raw", "--type=constant"}},
	"core_auto_update":   {},
	"protect_config":     {},
	"verify_core":        {},
	"search_engines_on":  {args: []string{"option", "update", "blog_public", "1"}},
	"search_engines_off": {args: []string{"option", "update", "blog_public", "0"}},
	"debug":              {items: "any"},
	"cron_takeover_on":   {},
	"cron_takeover_off":  {},
	"admin_password":     {items: "1"},
	"db_password":        {},
	"debug_log":          {},
}

func (o *Operation) validate(env *host.Env) error {
	if err := o.Target.validate(env); err != nil {
		return err
	}
	spec, ok := ops[o.Op]
	if !ok {
		return &validate.Error{Field: "op", Message: "is not a known operation"}
	}
	if len(o.Items) > 100 {
		return &validate.Error{Field: "items", Message: "at most 100"}
	}
	for _, it := range o.Items {
		if !slugRe.MatchString(it) {
			return &validate.Error{Field: "items", Message: "must be plugin or theme names"}
		}
	}
	switch spec.items {
	case "":
		if len(o.Items) > 0 {
			return &validate.Error{Field: "items", Message: "not used by " + o.Op}
		}
	case "1":
		if len(o.Items) != 1 {
			return &validate.Error{Field: "items", Message: "choose one"}
		}
	case "n":
		if len(o.Items) == 0 {
			return &validate.Error{Field: "items", Message: "choose at least one"}
		}
	}
	switch o.Op {
	case "debug":
		for _, it := range o.Items {
			if !contains(DebugConstants, it) {
				return &validate.Error{Field: "items", Message: it + " is not a debugging setting"}
			}
		}
	case "admin_password":
		if n, err := strconv.Atoi(o.Items[0]); err != nil || n < 1 {
			return &validate.Error{Field: "items", Message: "choose an administrator"}
		}
	}
	if o.Op == "admin_password" || o.Op == "db_password" {
		if !validSecret(o.Password) {
			return &validate.Error{Field: "password", Message: "8 to 128 characters, on one line"}
		}
	} else if o.Password != "" {
		return &validate.Error{Field: "password", Message: "not used by " + o.Op}
	}
	if o.Op == "core_auto_update" && o.Value != "minor" && o.Value != "major" && o.Value != "off" {
		return &validate.Error{Field: "value", Message: "must be minor, major or off"}
	}
	return nil
}

var checksumLine = regexp.MustCompile(`(?:File doesn't verify against checksum|File should not exist): (\S+)`)

// manage performs a quick operation, or returns a job function for a long one.
func manage(ctx context.Context, env *host.Env, o Operation) (map[string]any, func(context.Context, io.Writer) (any, error), error) {
	r, dir, err := prepare(ctx, env, o.Target)
	if err != nil {
		return nil, nil, err
	}
	spec := ops[o.Op]
	run := func(ctx context.Context, log io.Writer, args ...string) ([]byte, error) {
		return r.wp(ctx, dir, execx.Opts{Output: log, Timeout: 20 * time.Minute}, args...)
	}
	if spec.job {
		args := append([]string{}, spec.args...)
		if spec.items == "all" && len(o.Items) == 0 {
			args = append(args, "--all")
		}
		args = append(args, o.Items...)
		return nil, func(ctx context.Context, log io.Writer) (any, error) {
			if out, err := run(ctx, log, args...); err != nil {
				return nil, fmt.Errorf("%s", cliError(out))
			}
			if o.Op == "core_update" {
				if out, err := run(ctx, log, "core", "update-db"); err != nil {
					return nil, fmt.Errorf("the database update failed: %s", cliError(out))
				}
			}
			return map[string]any{"op": o.Op, "path": o.Path}, nil
		}, nil
	}
	if res, ok, err := settings(ctx, env, r, dir, o); ok {
		return res, nil, err
	}
	quick := func(args ...string) error {
		out, err := r.wp(ctx, dir, execx.Opts{}, args...)
		if err != nil && args[0] == "config" && strings.Contains(string(out), "transformation") {
			// A wp-config.php without WordPress's "That's all, stop
			// editing!" line: add the setting just before WordPress loads.
			out, err = r.wp(ctx, dir, execx.Opts{}, append(args, "--anchor=require_once ABSPATH . 'wp-settings.php';", "--placement=before")...)
		}
		if err != nil {
			return actions.Failed(cliError(out), "")
		}
		return nil
	}
	switch o.Op {
	case "core_auto_update":
		val := map[string][]string{"minor": {"minor"}, "major": {"true", "--raw"}, "off": {"false", "--raw"}}[o.Value]
		if err := quick(append([]string{"config", "set", "WP_AUTO_UPDATE_CORE", "--type=constant"}, val...)...); err != nil {
			return nil, nil, err
		}
	case "protect_config":
		out, err := r.wp(ctx, dir, execx.Opts{}, "config", "path")
		path := strings.TrimSpace(lastLine(out))
		if err != nil || !filepath.IsAbs(path) || filepath.Base(path) != "wp-config.php" || !strings.HasPrefix(filepath.Clean(path), r.root+"/") {
			return nil, nil, actions.Failed("wp-config.php was not found inside the website.", host.Output(out))
		}
		if _, _, err := r.script(ctx, `<?php exit(chmod(getenv('WHACP_FILE'), 0600) ? 0 : 1);`, []string{"WHACP_FILE=" + path}, time.Minute); err != nil {
			return nil, nil, actions.Failed("The permissions of wp-config.php could not be changed.", "")
		}
	case "verify_core":
		out, err := r.wp(ctx, dir, execx.Opts{Timeout: 5 * time.Minute}, "core", "verify-checksums")
		var files []string
		for _, m := range checksumLine.FindAllStringSubmatch(string(out), 200) {
			files = append(files, clip(m[1], 255))
		}
		if err != nil && len(files) == 0 {
			return nil, nil, actions.Failed("The core files could not be verified: "+cliError(out), "")
		}
		return map[string]any{"ok": err == nil, "files": files}, nil, nil
	default:
		if err := quick(append(append([]string{}, spec.args...), o.Items...)...); err != nil {
			return nil, nil, err
		}
	}
	return map[string]any{"op": o.Op}, nil, nil
}

func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return lines[len(lines)-1]
}

// loginTTL is how long a sign-in link works.
const loginTTL = time.Minute

// login writes a one-time sign-in file for an administrator.
func login(ctx context.Context, env *host.Env, t Target, adminID int) (map[string]any, error) {
	r, dir, err := prepare(ctx, env, t)
	if err != nil {
		return nil, err
	}
	out, err := r.wp(ctx, dir, execx.Opts{}, "user", "get", fmt.Sprint(adminID), "--field=roles")
	if err != nil || !strings.Contains(lastLine(out), "administrator") {
		return nil, actions.Conflict("That user is not an administrator of this WordPress.")
	}
	b := make([]byte, 16)
	rand.Read(b)
	name := "whacp-login-" + hex.EncodeToString(b) + ".php"
	expires := env.Now().Add(loginTTL)
	code := fmt.Sprintf(loginScript, expires.Unix(), expires.Unix(), adminID)
	if o, err := env.Runner.RunWith(ctx, "sh", execx.Opts{As: r.cred, Dir: dir, Stdin: strings.NewReader(code), Timeout: time.Minute}, "-c", "umask 077 && cat > "+name); err != nil {
		return nil, actions.Failed("The sign-in link could not be created.", host.Output(o))
	}
	// Removed when unused (the file also refuses to work once expired).
	cred := r.cred
	time.AfterFunc(loginTTL+30*time.Second, func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		env.Runner.RunWith(ctx, "sh", execx.Opts{As: cred, Dir: dir}, "-c", "rm -f "+name)
	})
	return map[string]any{"file": name, "expires_at": expires.Format(time.RFC3339)}, nil
}

// Register adds the wordpress.* actions.
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "wordpress.scan", Mutating: true, Timeout: 280 * time.Second, Description: "Find WordPress installations in websites (read-only; WordPress is not loaded).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Sites []Site `json:"sites"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if len(in.Sites) > 1000 {
			return nil, actions.InvalidParams("sites: at most 1000")
		}
		for i := range in.Sites {
			if err := in.Sites[i].validate(env); err != nil {
				return nil, host.Invalid(err)
			}
		}
		// Four websites at a time (the panel waits behind a 5-minute proxy).
		type res struct {
			found []Installation
			err   error
		}
		results := make([]res, len(in.Sites))
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for i, s := range in.Sites {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() { <-sem; wg.Done() }()
				f, err := scanSite(ctx, env, s)
				results[i] = res{f, err}
			}()
		}
		wg.Wait()
		installs := []Installation{}
		errs := []map[string]string{}
		for i, r := range results {
			if r.err != nil {
				errs = append(errs, map[string]string{"domain": in.Sites[i].Domain, "error": r.err.Error()})
				continue
			}
			installs = append(installs, r.found...)
		}
		return map[string]any{"installs": installs, "errors": errs, "latest": LatestVersion(ctx), "wp_cli": pharReady(env), "wp_cli_version": WPCLIVersion}, nil
	}})
	r.Register(actions.Action{Name: "wordpress.info", Mutating: true, Timeout: 3 * time.Minute, Description: "Overview of a WordPress installation: versions, updates, plugins, themes, settings (WP-CLI as the website account).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Target
			Force bool `json:"force"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.Target.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		return info(ctx, env, in.Target, in.Force)
	}})
	r.Register(actions.Action{Name: "wordpress.manage", Mutating: true, Timeout: 6 * time.Minute, Description: "Change a WordPress installation: updates (job), plugins, themes, maintenance mode, hardening, core file verification.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in Operation
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		res, job, err := manage(ctx, env, in)
		if err != nil || job == nil {
			return res, err
		}
		j := env.Jobs.Start("wordpress."+in.Op, "wordpress:"+in.Domain+"/"+in.Path, job)
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "wordpress.security", Mutating: true, Timeout: 4 * time.Minute, Description: "Security hardening of a WordPress installation: the state of each measure (status), or apply / roll back measures. Web server rules are checked with nginx -t; WordPress settings are changed with WP-CLI as the website account and what they replaced is recorded for the rollback.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in SecurityRequest
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.Target.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		return security(ctx, env, in)
	}})
	r.Register(actions.Action{Name: "wordpress.copy", Mutating: true, Timeout: 2 * time.Minute, Description: "Staging and cloning (job): clone an installation to another folder with its own database, sync files and/or the database from one installation to another, or put back a restore point. Files are copied by tar as the source and target accounts; a restore point of the target is saved before anything is replaced.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in CopyRequest
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		job, err := copyJob(env, in)
		if err != nil {
			return nil, err
		}
		j := env.Jobs.Start("wordpress."+in.Mode, "wordpress:"+in.Dest.Domain+"/"+in.Dest.Path, job)
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "wordpress.restore_points", Description: "Restore points of a website's WordPress installations (saved before a sync or clone replaced them), newest first.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domain string `json:"domain"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("domain", in.Domain); err != nil {
			return nil, host.Invalid(err)
		}
		return map[string]any{"restore_points": ListRestorePoints(env, in.Domain)}, nil
	}})
	r.Register(actions.Action{Name: "wordpress.login", Mutating: true, Timeout: 2 * time.Minute, Description: "One-time sign-in link for a WordPress administrator (a file that deletes itself on first use and expires after a minute).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Target
			AdminID int `json:"admin_id"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.Target.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		if in.AdminID < 1 || in.AdminID > 1<<31 {
			return nil, actions.InvalidParams("admin_id: choose an administrator")
		}
		return login(ctx, env, in.Target, in.AdminID)
	}})
}
