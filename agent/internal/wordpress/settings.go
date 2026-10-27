package wordpress

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// DebugConstants are the debugging settings the toolkit switches.
var DebugConstants = []string{"WP_DEBUG", "WP_DEBUG_LOG", "WP_DEBUG_DISPLAY", "SCRIPT_DEBUG", "SAVEQUERIES"}

// phpString is s as a PHP single-quoted string literal.
func phpString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// validSecret is a password the toolkit passes on (never logged, never in
// a command line: only inside the program sent on standard input).
func validSecret(s string) bool {
	return len(s) >= 8 && len(s) <= 128 && !strings.ContainsAny(s, "\x00\r\n")
}

// setConstant sets a wp-config.php constant (raw PHP value).
func (r *runner) setConstant(ctx context.Context, dir, name, raw string) error {
	args := []string{"config", "set", name, raw, "--raw", "--type=constant"}
	out, err := r.wp(ctx, dir, execx.Opts{}, args...)
	if err != nil && strings.Contains(string(out), "transformation") {
		out, err = r.wp(ctx, dir, execx.Opts{}, append(args, "--anchor=require_once ABSPATH . 'wp-settings.php';", "--placement=before")...)
	}
	if err != nil {
		return actions.Failed(cliError(out), "")
	}
	return nil
}

// deleteConstant removes a wp-config.php constant (none is fine).
func (r *runner) deleteConstant(ctx context.Context, dir, name string) error {
	if _, err := r.wp(ctx, dir, execx.Opts{}, "config", "has", name, "--type=constant"); err != nil {
		return nil
	}
	if out, err := r.wp(ctx, dir, execx.Opts{}, "config", "delete", name, "--type=constant"); err != nil {
		return actions.Failed(cliError(out), "")
	}
	return nil
}

// evalResult runs a program inside WordPress (plugins and themes not
// loaded) and reads its NIMVOR-JSON line.
func (r *runner) evalResult(ctx context.Context, dir, code string, v any) error {
	stdout := &limited{n: 4 << 20}
	errOut, err := r.wp(ctx, dir, execx.Opts{Stdin: strings.NewReader(code), Stdout: stdout, Timeout: 2 * time.Minute}, "eval-file", "-")
	if rerr := result(stdout.Bytes(), v); rerr != nil {
		if err == nil {
			err = rerr
		}
		return actions.Failed("WordPress did not answer: "+cliError(append(errOut, stdout.Bytes()...)), "")
	}
	return nil
}

// phpPath is the PHP command line for cron (Debian or Remi layout).
func phpPath(env *host.Env, version string) string {
	remi := "/opt/remi/php" + strings.ReplaceAll(version, ".", "") + "/root/usr/bin/php"
	if !env.Root.Exists("/usr/bin/php"+version) && env.Root.Exists(remi) {
		return remi
	}
	return "/usr/bin/php" + version
}

// CronCommand runs WordPress's due scheduled tasks (the system cron job
// that takes over from wp-cron.php). It runs as the website account.
func CronCommand(env *host.Env, t Target) string {
	return phpPath(env, t.PHPVersion) + " " + WPCLIPath + " --path=" + filepath.Join(t.Root, t.Path) + " cron event run --due-now --quiet"
}

const adminPasswordScript = `<?php
$u = get_user_by('id', %d);
if (!$u || !user_can($u, 'manage_options')) {
    echo "\nNIMVOR-JSON " . json_encode(['ok' => false, 'error' => 'That user is not an administrator of this WordPress.']) . "\n";
    return;
}
wp_set_password(%s, $u->ID);
if (class_exists('WP_Session_Tokens')) {
    WP_Session_Tokens::get_instance($u->ID)->destroy_all();
}
echo "\nNIMVOR-JSON " . json_encode(['ok' => true, 'login' => $u->user_login]) . "\n";
`

// dbPasswordScript changes the database password as the database user
// itself (MariaDB and MySQL let an account change its own password), then
// writes it to wp-config.php; if that fails the old password is set back.
const dbPasswordScript = `<?php
global $wpdb;
$new = %s;
$fail = function (string $m) { echo "\nNIMVOR-JSON " . json_encode(['ok' => false, 'error' => $m]) . "\n"; };
$cfg = is_file(ABSPATH . 'wp-config.php') ? ABSPATH . 'wp-config.php' : dirname(ABSPATH) . '/wp-config.php';
$src = (string) @file_get_contents($cfg);
$re = '/define\s*\(\s*([\'"])DB_PASSWORD\1\s*,\s*(\'(?:\\\\.|[^\'\\\\])*\'|"(?:\\\\.|[^"\\\\])*")\s*\)\s*;/';
if (preg_match_all($re, $src) !== 1) {
    $fail('wp-config.php does not set DB_PASSWORD in the usual way; change it by hand.');
    return;
}
$out = preg_replace_callback($re, fn () => "define( 'DB_PASSWORD', " . var_export($new, true) . " );", $src, 1);
$wpdb->suppress_errors(true);
$set = function (string $p) use ($wpdb) {
    if ($wpdb->query($wpdb->prepare('SET PASSWORD = PASSWORD(%%s)', $p)) !== false) {
        return true;
    }
    return $wpdb->query($wpdb->prepare('ALTER USER CURRENT_USER() IDENTIFIED BY %%s', $p)) !== false;
};
if (!$set($new)) {
    $fail('The database server refused the password change.');
    return;
}
$tmp = $cfg . '.nimvor-' . bin2hex(random_bytes(6));
$ok = @file_put_contents($tmp, $out) === strlen($out) && @chmod($tmp, fileperms($cfg) & 0777) && @rename($tmp, $cfg);
if (!$ok) {
    @unlink($tmp);
    $set(DB_PASSWORD);
    $fail('wp-config.php could not be written; the database password was not changed.');
    return;
}
echo "\nNIMVOR-JSON " . json_encode(['ok' => true, 'user' => DB_USER]) . "\n";
`

// debugLogScript returns the end of WordPress's debug log (inside the
// website only).
const debugLogScript = `<?php
$root = rtrim((string) getenv('NIMVOR_ROOT'), '/') . '/';
$p = defined('WP_DEBUG_LOG') && is_string(WP_DEBUG_LOG) && WP_DEBUG_LOG !== '' && !in_array(strtolower(WP_DEBUG_LOG), ['1', 'true'], true) ? WP_DEBUG_LOG : WP_CONTENT_DIR . '/debug.log';
$real = realpath($p);
$out = ['path' => $p, 'exists' => false, 'lines' => [], 'size' => 0];
if ($real !== false && strpos($real, $root) === 0 && is_file($real) && !is_link($p)) {
    $size = filesize($real);
    $f = fopen($real, 'r');
    fseek($f, max(0, $size - 262144));
    $data = (string) stream_get_contents($f);
    fclose($f);
    $lines = preg_split('/\r?\n/', rtrim($data));
    if ($size > 262144) {
        array_shift($lines);
    }
    $out = ['path' => $p, 'exists' => true, 'lines' => array_slice($lines, -500), 'size' => $size];
}
echo "\nNIMVOR-JSON " . json_encode($out, JSON_INVALID_UTF8_SUBSTITUTE) . "\n";
`

// settings handles the operations of this file; ok=false for others.
func settings(ctx context.Context, env *host.Env, r *runner, dir string, o Operation) (map[string]any, bool, error) {
	switch o.Op {
	case "debug":
		on := map[string]bool{}
		for _, it := range o.Items {
			on[it] = true
		}
		for _, c := range DebugConstants {
			if err := r.setConstant(ctx, dir, c, strconv.FormatBool(on[c])); err != nil {
				return nil, true, err
			}
		}
		return map[string]any{"op": o.Op, "enabled": o.Items}, true, nil
	case "cron_takeover_on":
		if err := r.setConstant(ctx, dir, "DISABLE_WP_CRON", "true"); err != nil {
			return nil, true, err
		}
		return map[string]any{"op": o.Op, "command": CronCommand(env, o.Target)}, true, nil
	case "cron_takeover_off":
		if err := r.setConstant(ctx, dir, "DISABLE_WP_CRON", "false"); err != nil {
			return nil, true, err
		}
		return map[string]any{"op": o.Op}, true, nil
	case "admin_password":
		id, _ := strconv.Atoi(o.Items[0])
		var res struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
			Login string `json:"login"`
		}
		if err := r.evalResult(ctx, dir, fmt.Sprintf(adminPasswordScript, id, phpString(o.Password)), &res); err != nil {
			return nil, true, err
		}
		if !res.OK {
			return nil, true, actions.Conflict(res.Error)
		}
		return map[string]any{"op": o.Op, "login": clip(res.Login, 60)}, true, nil
	case "db_password":
		var res struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
			User  string `json:"user"`
		}
		if err := r.evalResult(ctx, dir, fmt.Sprintf(dbPasswordScript, phpString(o.Password)), &res); err != nil {
			return nil, true, err
		}
		if !res.OK {
			return nil, true, actions.Failed(res.Error, "")
		}
		return map[string]any{"op": o.Op, "user": clip(res.User, 80)}, true, nil
	case "debug_log":
		var res struct {
			Path   string   `json:"path"`
			Exists bool     `json:"exists"`
			Lines  []string `json:"lines"`
			Size   int64    `json:"size"`
		}
		stdout := &limited{n: 4 << 20}
		errOut, err := r.wp(ctx, dir, execx.Opts{Stdin: strings.NewReader(debugLogScript), Stdout: stdout, Env: []string{"NIMVOR_ROOT=" + r.root}}, "eval-file", "-")
		if rerr := result(stdout.Bytes(), &res); rerr != nil {
			return nil, true, actions.Failed("The debug log could not be read: "+host.Why(errOut, err), "")
		}
		for i := range res.Lines {
			res.Lines[i] = clip(res.Lines[i], 2000)
		}
		rel, _ := filepath.Rel(r.root, res.Path)
		return map[string]any{"op": o.Op, "path": clip(rel, 255), "exists": res.Exists, "lines": res.Lines, "size": res.Size}, true, nil
	}
	return nil, false, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
