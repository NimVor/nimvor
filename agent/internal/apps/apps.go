// Package apps installs applications into websites: WordPress (downloaded
// over HTTPS and verified against the published SHA-1) and Laravel (via
// Composer).
//
// Only the download runs as root, into the agent's own state directory.
// Extracting, writing configuration, Composer and PHP all run as the
// website account. WordPress is installed immediately from the command
// line so its web installer can never be claimed by someone else.
package apps

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/sites"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// Sources says where applications are downloaded from.
type Sources struct {
	WordPressURL string
	Client       *http.Client
}

// DefaultSources are the official download locations.
var DefaultSources = Sources{WordPressURL: "https://wordpress.org/latest.tar.gz", Client: &http.Client{Timeout: 5 * time.Minute}}

const maxDownload = 200 << 20

// DB holds database credentials for the application.
type DB struct {
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
	Host     string `json:"host"`
}

// Request describes an installation.
type Request struct {
	App        string `json:"app"` // wordpress, laravel
	Domain     string `json:"domain"`
	User       string `json:"user"`
	Root       string `json:"root"`
	Path       string `json:"path"` // relative install folder
	PHPVersion string `json:"php_version"`
	DB         DB     `json:"db"`
	URL        string `json:"url"` // https://example.com
	// WordPress
	Title         string `json:"title"`
	AdminUser     string `json:"admin_user"`
	AdminEmail    string `json:"admin_email"`
	AdminPassword string `json:"admin_password"`
	// Laravel
	Migrate bool `json:"migrate"`
}

var (
	wpUser = regexp.MustCompile(`^[A-Za-z0-9._@-]{3,60}$`)
	urlRe  = regexp.MustCompile(`^https?://[A-Za-z0-9.-]+(:[0-9]+)?$`)
)

func (r *Request) validate(env *host.Env) error {
	if r.App != "wordpress" && r.App != "laravel" {
		return &validate.Error{Field: "app", Message: "must be wordpress or laravel"}
	}
	if err := validate.Domain("domain", r.Domain); err != nil {
		return err
	}
	if err := validate.Username("user", r.User); err != nil {
		return err
	}
	if r.Root == "" {
		r.Root = filepath.Join(env.Cfg.SitesRoot, r.Domain)
	}
	if err := validate.Within("root", env.Cfg.SitesRoot, r.Root); err != nil {
		return err
	}
	if r.Path == "" {
		return &validate.Error{Field: "path", Message: "choose the folder to install into"}
	}
	if err := validate.RelPath("path", r.Path); err != nil {
		return err
	}
	switch strings.Split(filepath.Clean(r.Path), "/")[0] {
	case "logs", "tmp", "backups", ".ssh":
		return &validate.Error{Field: "path", Message: "cannot install into " + r.Path}
	}
	if err := validate.PHPVersion("php_version", r.PHPVersion); err != nil {
		return err
	}
	if err := validate.DBIdentifier("db.name", r.DB.Name); err != nil {
		return err
	}
	if err := validate.DBIdentifier("db.user", r.DB.User); err != nil {
		return err
	}
	if r.DB.Password == "" || len(r.DB.Password) > 200 || strings.ContainsAny(r.DB.Password, "\x00\n\r") {
		return &validate.Error{Field: "db.password", Message: "is required"}
	}
	if r.DB.Host == "" {
		r.DB.Host = "localhost"
	}
	if r.DB.Host != "localhost" && r.DB.Host != "127.0.0.1" {
		return &validate.Error{Field: "db.host", Message: "must be localhost"}
	}
	if !urlRe.MatchString(r.URL) {
		return &validate.Error{Field: "url", Message: "must be the website address, e.g. https://example.com"}
	}
	if r.App == "wordpress" {
		if len(r.Title) == 0 || len(r.Title) > 100 || strings.ContainsAny(r.Title, "\x00\n\r") {
			return &validate.Error{Field: "title", Message: "1–100 characters"}
		}
		if !wpUser.MatchString(r.AdminUser) {
			return &validate.Error{Field: "admin_user", Message: "3–60 letters, digits or . _ @ -"}
		}
		if !regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`).MatchString(r.AdminEmail) || len(r.AdminEmail) > 100 {
			return &validate.Error{Field: "admin_email", Message: "must be an email address"}
		}
		if err := validate.Password("admin_password", r.AdminPassword, 12); err != nil {
			return err
		}
	}
	return nil
}

// emptyTarget reports whether dir is missing, empty, or holds only WhaCP's
// placeholder page.
func emptyTarget(env *host.Env, r Request, dir string) (bool, error) {
	entries, err := env.Root.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Name() == "index.html" {
			b, _ := env.Root.ReadFile(filepath.Join(dir, "index.html"))
			if string(b) == sites.DefaultIndex(r.Domain) {
				continue
			}
		}
		return false, nil
	}
	return true, nil
}

// download fetches url to a root-only temporary file and returns its path
// and SHA-1.
func download(ctx context.Context, env *host.Env, client *http.Client, url string, log io.Writer) (string, string, error) {
	dir := filepath.Join(env.Cfg.StateDir, "downloads")
	if _, err := env.Writer.MkdirAll(dir, 0o700, agentfs.RootOwner); err != nil {
		return "", "", err
	}
	real, err := env.Root.Path(dir)
	if err != nil {
		return "", "", err
	}
	f, err := os.CreateTemp(real, "app-*.tar.gz")
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	fmt.Fprintf(log, "Downloading %s…\n", url)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "WhaCP")
	res, err := client.Do(req)
	if err != nil {
		os.Remove(f.Name())
		return "", "", fmt.Errorf("download failed: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		os.Remove(f.Name())
		return "", "", fmt.Errorf("download failed: HTTP %d", res.StatusCode)
	}
	h := sha1.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, maxDownload+1))
	if err != nil || n > maxDownload {
		os.Remove(f.Name())
		return "", "", fmt.Errorf("download failed or too large")
	}
	return f.Name(), hex.EncodeToString(h.Sum(nil)), nil
}

func fetchText(ctx context.Context, client *http.Client, url string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "WhaCP")
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	return strings.TrimSpace(string(b)), err
}

func salt() string {
	b := make([]byte, 48)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// phpString quotes s for a single-quoted PHP string literal.
func phpString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// WPConfig renders wp-config.php.
func WPConfig(r Request) string {
	var b strings.Builder
	b.WriteString("<?php\n// Generated by WhaCP.\n")
	fmt.Fprintf(&b, "define('DB_NAME', %s);\ndefine('DB_USER', %s);\ndefine('DB_PASSWORD', %s);\ndefine('DB_HOST', %s);\n", phpString(r.DB.Name), phpString(r.DB.User), phpString(r.DB.Password), phpString(r.DB.Host))
	b.WriteString("define('DB_CHARSET', 'utf8mb4');\ndefine('DB_COLLATE', '');\n")
	for _, k := range []string{"AUTH_KEY", "SECURE_AUTH_KEY", "LOGGED_IN_KEY", "NONCE_KEY", "AUTH_SALT", "SECURE_AUTH_SALT", "LOGGED_IN_SALT", "NONCE_SALT"} {
		fmt.Fprintf(&b, "define('%s', %s);\n", k, phpString(salt()))
	}
	b.WriteString("$table_prefix = 'wp_';\n")
	b.WriteString("define('DISALLOW_FILE_EDIT', true);\ndefine('WP_DEBUG', false);\n")
	b.WriteString("if (!defined('ABSPATH')) {\n    define('ABSPATH', __DIR__ . '/');\n}\nrequire_once ABSPATH . 'wp-settings.php';\n")
	return b.String()
}

// wpInstall runs WordPress's installer from the command line.
const wpInstall = `<?php
define('WP_INSTALLING', true);
$u = parse_url(getenv('WHACP_URL'));
$_SERVER['HTTP_HOST'] = $u['host'] . (isset($u['port']) ? ':' . $u['port'] : '');
$_SERVER['REQUEST_URI'] = '/';
$_SERVER['HTTPS'] = $u['scheme'] === 'https' ? 'on' : '';
// No "new site" e-mail: the panel shows the login details, and the local
// sendmail cannot queue mail from the installer's confined unit (Postfix's
// setgid postdrop is refused under NoNewPrivileges and retries forever).
function wp_new_blog_notification() {}
require getcwd() . '/wp-load.php';
require_once ABSPATH . 'wp-admin/includes/upgrade.php';
if (is_blog_installed()) { fwrite(STDERR, "WordPress is already installed.\n"); exit(1); }
$r = wp_install(getenv('WHACP_TITLE'), getenv('WHACP_ADMIN_USER'), getenv('WHACP_ADMIN_EMAIL'), true, '', wp_slash(getenv('WHACP_ADMIN_PASSWORD')));
update_option('siteurl', getenv('WHACP_URL'));
update_option('home', getenv('WHACP_URL'));
echo "WordPress installed (user id " . $r['user_id'] . ").\n";
`

// laravelEnv rewrites DB_* and APP_URL in a Laravel .env file.
const laravelEnv = `<?php
$f = getcwd() . '/.env';
$env = file_get_contents($f);
$set = ['DB_CONNECTION' => 'mysql', 'DB_HOST' => getenv('WHACP_DB_HOST'), 'DB_PORT' => '3306', 'DB_DATABASE' => getenv('WHACP_DB_NAME'), 'DB_USERNAME' => getenv('WHACP_DB_USER'), 'DB_PASSWORD' => getenv('WHACP_DB_PASSWORD'), 'APP_URL' => getenv('WHACP_URL'), 'APP_ENV' => 'production', 'APP_DEBUG' => 'false'];
foreach ($set as $k => $v) {
    $line = $k . '=' . (preg_match('/[\s#"\'$]/', $v) ? '"' . addcslashes($v, '"\\$') . '"' : $v);
    $n = 0;
    $env = preg_replace('/^#?\s*' . $k . '=.*$/m', str_replace(['\\', '$'], ['\\\\', '\\$'], $line), $env, 1, $n);
    if ($n === 0) { $env .= "\n" . $line; }
}
file_put_contents($f, $env);
chmod($f, 0640);
echo "Configured .env\n";
`

// Install performs the installation (called inside a job).
func Install(ctx context.Context, env *host.Env, src Sources, r Request, cred *execx.Credential, log io.Writer) (map[string]any, error) {
	root, _ := env.Root.Path(r.Root)
	target := filepath.Join(root, r.Path)
	php := "php" + r.PHPVersion
	as := func(dir string, extraEnv []string, stdin string) execx.Opts {
		o := execx.Opts{As: cred, Dir: dir, Output: log, Env: extraEnv, Timeout: 20 * time.Minute}
		if stdin != "" {
			o.Stdin = strings.NewReader(stdin)
		}
		return o
	}
	// Check PHP extensions first, so nothing is downloaded or written in vain.
	need := map[string][]string{"wordpress": {"mysqli", "json"}, "laravel": {"pdo_mysql", "mbstring", "xml", "curl", "zip"}}[r.App]
	check := fmt.Sprintf(`$m = array_filter(%s, fn ($e) => !extension_loaded($e)); if ($m) { echo implode(" ", $m); exit(1); }`, phpArray(need))
	if out, err := env.Runner.RunWith(ctx, php, execx.Opts{As: cred, Dir: root, Timeout: time.Minute}, "-r", check); err != nil {
		missing := strings.TrimSpace(string(out))
		return nil, fmt.Errorf("PHP %s is missing extensions: %s. Install them under PHP and try again", r.PHPVersion, missing)
	}
	mkdir := fmt.Sprintf("umask 027 && mkdir -p %s", shQuote(target))
	if _, err := env.Runner.RunWith(ctx, "sh", as(root, nil, ""), "-c", mkdir); err != nil {
		return nil, fmt.Errorf("the folder %s could not be created", r.Path)
	}

	switch r.App {
	case "wordpress":
		sumURL := src.WordPressURL + ".sha1"
		want, err := fetchText(ctx, src.Client, sumURL)
		if err != nil || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(want) {
			return nil, fmt.Errorf("the WordPress checksum could not be downloaded")
		}
		file, got, err := download(ctx, env, src.Client, src.WordPressURL, log)
		if err != nil {
			return nil, err
		}
		defer os.Remove(file)
		if got != want {
			return nil, fmt.Errorf("the WordPress download does not match its published checksum")
		}
		fmt.Fprintln(log, "Checksum verified.")
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		// Remove WhaCP's placeholder page, then extract as the site account.
		_, _ = env.Runner.RunWith(ctx, "sh", as(target, nil, ""), "-c", "rm -f index.html")
		o := as(target, nil, "")
		o.Stdin = f
		if out, err := env.Runner.RunWith(ctx, "tar", o, "-xzf", "-", "--no-same-owner", "--strip-components=1", "-C", target); err != nil {
			return nil, fmt.Errorf("extracting WordPress failed: %s", host.Output(out))
		}
		if _, err := env.Runner.RunWith(ctx, "sh", as(target, nil, WPConfig(r)), "-c", "umask 027 && cat > wp-config.php"); err != nil {
			return nil, fmt.Errorf("wp-config.php could not be written")
		}
		fmt.Fprintln(log, "Wrote wp-config.php (file editing in the dashboard is disabled).")
		wpEnv := []string{"WHACP_URL=" + r.URL, "WHACP_TITLE=" + r.Title, "WHACP_ADMIN_USER=" + r.AdminUser, "WHACP_ADMIN_EMAIL=" + r.AdminEmail, "WHACP_ADMIN_PASSWORD=" + r.AdminPassword}
		if out, err := env.Runner.RunWith(ctx, php, as(target, wpEnv, wpInstall), "-d", "display_errors=stderr"); err != nil {
			return nil, fmt.Errorf("the WordPress installer failed: %s", host.Output(out))
		}
		return map[string]any{"app": "wordpress", "path": r.Path, "admin_user": r.AdminUser, "login_url": r.URL + "/wp-admin/"}, nil

	case "laravel":
		if !env.Runner.Available("composer") {
			return nil, fmt.Errorf("composer is not installed; install it under Packages")
		}
		parent, name := filepath.Split(target)
		// create-project needs a missing or empty target folder.
		_, _ = env.Runner.RunWith(ctx, "sh", as(root, nil, ""), "-c", "rmdir "+shQuote(target)+" 2>/dev/null; true")
		cenv := []string{"COMPOSER_NO_INTERACTION=1", "COMPOSER_HOME=" + filepath.Join(root, ".composer"), "COMPOSER_CACHE_DIR=" + filepath.Join(root, "tmp", "composer-cache")}
		opts := as(parent, cenv, "")
		opts.Timeout = 30 * time.Minute
		composer := "/usr/bin/composer"
		if env.Root.Exists("/usr/local/bin/composer") {
			composer = "/usr/local/bin/composer"
		}
		// Run Composer with the website's PHP version, as the website account
		// (php8.3 on Debian, Remi's /opt/remi/php83/root/usr/bin/php on RHEL).
		phpBin := "php" + r.PHPVersion
		for _, v := range env.Discovery.PHP().Versions {
			if v.Version == r.PHPVersion && v.CLIBinary != "" {
				phpBin = v.CLIBinary
			}
		}
		script := fmt.Sprintf("exec %s %s create-project --prefer-dist --no-progress laravel/laravel %s", shQuote(phpBin), composer, shQuote(filepath.Clean(name)))
		if out, err := env.Runner.RunWith(ctx, "sh", opts, "-c", script); err != nil {
			return nil, fmt.Errorf("composer create-project failed: %s", host.Output(out))
		}
		lenv := []string{"WHACP_URL=" + r.URL, "WHACP_DB_HOST=" + r.DB.Host, "WHACP_DB_NAME=" + r.DB.Name, "WHACP_DB_USER=" + r.DB.User, "WHACP_DB_PASSWORD=" + r.DB.Password}
		if out, err := env.Runner.RunWith(ctx, php, as(target, lenv, laravelEnv)); err != nil {
			return nil, fmt.Errorf("configuring .env failed: %s", host.Output(out))
		}
		if r.Migrate {
			if out, err := env.Runner.RunWith(ctx, php, as(target, nil, ""), "artisan", "migrate", "--force"); err != nil {
				return nil, fmt.Errorf("php artisan migrate failed: %s", host.Output(out))
			}
		}
		return map[string]any{"app": "laravel", "path": r.Path, "document_root": filepath.Join(r.Path, "public")}, nil
	}
	return nil, fmt.Errorf("unknown app")
}

func phpArray(items []string) string {
	q := make([]string, len(items))
	for i, it := range items {
		q[i] = phpString(it)
	}
	return "[" + strings.Join(q, ", ") + "]"
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Register adds apps.install.
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "apps.install", Mutating: true, Description: "Install WordPress or Laravel into a website (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in Request
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		cred, _, err := env.Credential(in.User)
		if err != nil {
			return nil, actions.InvalidParams("user: " + err.Error())
		}
		empty, err := emptyTarget(env, in, filepath.Join(in.Root, in.Path))
		if err != nil {
			return nil, actions.Failed("The target folder could not be read.", err.Error())
		}
		if !empty {
			return nil, actions.Conflict(fmt.Sprintf("The folder %s is not empty. Choose an empty folder, or remove its files first.", in.Path))
		}
		j := env.Jobs.Start("apps.install", "site:"+in.Domain, func(ctx context.Context, log io.Writer) (any, error) {
			return Install(ctx, env, DefaultSources, in, cred, log)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
}
