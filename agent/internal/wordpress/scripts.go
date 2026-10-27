package wordpress

// The PHP programs the agent runs as the website account. They come from
// the agent (standard input), never from the website, and print one line
// "NIMVOR-JSON {…}" that the agent reads; anything else a website prints is
// ignored. Neither prints database passwords.

// marker starts the result line.
const marker = "NIMVOR-JSON "

// scanScript finds WordPress under a website folder without loading
// WordPress: version.php and wp-config.php are only read, as text.
const scanScript = `<?php
error_reporting(0);
$root = rtrim((string) getenv('NIMVOR_ROOT'), '/');
$top = ['logs' => 1, 'tmp' => 1, 'backups' => 1, '.ssh' => 1, '.composer' => 1];
$skip = ['node_modules' => 1, 'vendor' => 1, '.git' => 1, 'wp-content' => 1, 'wp-includes' => 1, 'wp-admin' => 1, 'cgi-bin' => 1];
$found = [];
$visited = 0;
$walk = function (string $dir, int $depth) use (&$walk, &$found, &$visited, $top, $skip) {
    if (++$visited > 5000 || count($found) >= 50) {
        return;
    }
    if (is_file($dir . '/wp-includes/version.php') && is_file($dir . '/wp-settings.php')) {
        $found[] = $dir;
    }
    if ($depth >= 4 || !($h = opendir($dir))) {
        return;
    }
    while (($e = readdir($h)) !== false) {
        if ($e === '.' || $e === '..' || isset($skip[$e]) || ($depth === 0 && isset($top[$e]))) {
            continue;
        }
        $p = $dir . '/' . $e;
        if (!is_link($p) && is_dir($p)) {
            $walk($p, $depth + 1);
        }
    }
    closedir($h);
};
$walk($root, 0);
function nimvor_const(string $c, string $name) {
    if (!preg_match('/define\s*\(\s*[\'"]' . $name . '[\'"]\s*,\s*(?:[\'"]([^\'"]*)[\'"]|(true|false|[0-9]+))/i', $c, $m)) {
        return null;
    }
    return isset($m[2]) && $m[2] !== '' ? strtolower($m[2]) : $m[1];
}
$out = [];
foreach ($found as $dir) {
    // One-time sign-in files left unused.
    foreach (glob($dir . '/nimvor-login-*.php') ?: [] as $f) {
        if (filemtime($f) < time() - 300) {
            unlink($f);
        }
    }
    $v = '';
    if (preg_match('/\$wp_version\s*=\s*[\'"]([^\'"]+)[\'"]/', (string) file_get_contents($dir . '/wp-includes/version.php', false, null, 0, 65536), $m)) {
        $v = $m[1];
    }
    $cfg = $dir . '/wp-config.php';
    if (!is_file($cfg) && is_file(dirname($dir) . '/wp-config.php') && !is_file(dirname($dir) . '/wp-settings.php')) {
        $cfg = dirname($dir) . '/wp-config.php';
    }
    $c = is_file($cfg) ? (string) file_get_contents($cfg, false, null, 0, 262144) : '';
    $prefix = preg_match('/\$table_prefix\s*=\s*[\'"]([A-Za-z0-9_]*)[\'"]/', $c, $m) ? $m[1] : '';
    $out[] = [
        'dir' => $dir, 'version' => $v, 'configured' => $c !== '',
        'db_name' => (string) nimvor_const($c, 'DB_NAME'), 'db_host' => (string) nimvor_const($c, 'DB_HOST'), 'table_prefix' => $prefix,
        'debug' => nimvor_const($c, 'WP_DEBUG') === 'true', 'file_edit_disabled' => nimvor_const($c, 'DISALLOW_FILE_EDIT') === 'true',
        'config_mode' => $c !== '' ? sprintf('%04o', fileperms($cfg) & 0777) : '',
        'maintenance' => is_file($dir . '/.maintenance'),
    ];
}
echo "\nNIMVOR-JSON " . json_encode(['installs' => $out, 'truncated' => $visited > 5000], JSON_INVALID_UTF8_SUBSTITUTE) . "\n";
`

// infoScript gathers an installation's overview inside WordPress (run with
// "wp eval-file -", plugins and themes not loaded). NIMVOR_FORCE=1 asks
// WordPress.org for updates now instead of using its 12-hour cache.
const infoScript = `<?php
require_once ABSPATH . 'wp-admin/includes/plugin.php';
require_once ABSPATH . 'wp-admin/includes/update.php';
require_once ABSPATH . 'wp-admin/includes/theme.php';
global $wpdb, $wp_version;
if (getenv('NIMVOR_FORCE') === '1') {
    delete_site_transient('update_core');
    delete_site_transient('update_plugins');
    delete_site_transient('update_themes');
}
wp_version_check();
wp_update_plugins();
wp_update_themes();
$core = get_site_transient('update_core');
$latest = '';
$phpNeeded = '';
if (is_object($core) && !empty($core->updates)) {
    foreach ($core->updates as $u) {
        if (isset($u->response) && in_array($u->response, ['upgrade', 'latest'], true) && version_compare($u->current, $latest, '>')) {
            $latest = (string) $u->current;
            $phpNeeded = (string) ($u->php_version ?? '');
        }
    }
}
$pu = get_site_transient('update_plugins');
$autoP = (array) get_site_option('auto_update_plugins', []);
$active = (array) get_option('active_plugins', []);
if (is_multisite()) {
    $active = array_merge($active, array_keys((array) get_site_option('active_sitewide_plugins', [])));
}
$plugins = [];
foreach (get_plugins() as $file => $p) {
    $name = dirname($file) === '.' ? basename($file, '.php') : dirname($file);
    $up = is_object($pu) && isset($pu->response[$file]) ? (string) $pu->response[$file]->new_version : '';
    $plugins[] = ['name' => $name, 'title' => (string) $p['Name'], 'version' => (string) $p['Version'], 'active' => in_array($file, $active, true),
        'update' => $up, 'auto_update' => in_array($file, $autoP, true), 'requires_php' => (string) ($p['RequiresPHP'] ?? '')];
}
foreach (get_mu_plugins() as $file => $p) {
    $plugins[] = ['name' => basename($file, '.php'), 'title' => (string) $p['Name'], 'version' => (string) $p['Version'], 'active' => true, 'update' => '', 'auto_update' => false, 'must_use' => true, 'requires_php' => ''];
}
$tu = get_site_transient('update_themes');
$autoT = (array) get_site_option('auto_update_themes', []);
$themes = [];
foreach (wp_get_themes() as $slug => $t) {
    $themes[] = ['name' => (string) $slug, 'title' => (string) $t->get('Name'), 'version' => (string) $t->get('Version'),
        'active' => $slug === get_stylesheet(), 'parent' => $slug === get_template() && get_template() !== get_stylesheet(),
        'update' => is_object($tu) && isset($tu->response[$slug]) ? (string) $tu->response[$slug]['new_version'] : '',
        'auto_update' => in_array($slug, $autoT, true)];
}
$admins = [];
foreach (get_users(['role' => 'administrator', 'number' => 20, 'orderby' => 'ID', 'fields' => ['ID', 'user_login']]) as $u) {
    $admins[] = ['id' => (int) $u->ID, 'login' => (string) $u->user_login];
}
$size = $wpdb->get_row($wpdb->prepare('SELECT COUNT(*) AS n, COALESCE(SUM(data_length + index_length), 0) AS bytes FROM information_schema.TABLES WHERE table_schema = %s AND table_name LIKE %s', DB_NAME, $wpdb->esc_like($wpdb->base_prefix) . '%'));
$cfg = is_file(ABSPATH . 'wp-config.php') ? ABSPATH . 'wp-config.php' : dirname(ABSPATH) . '/wp-config.php';
$const = fn ($c) => defined($c) ? constant($c) : null;
echo "\nNIMVOR-JSON " . json_encode([
    'version' => (string) $wp_version, 'php' => PHP_VERSION, 'mysql' => (string) $wpdb->db_version(), 'multisite' => is_multisite(),
    'siteurl' => (string) get_option('siteurl'), 'home' => (string) get_option('home'), 'title' => (string) get_option('blogname'),
    'search_engines' => (string) get_option('blog_public') === '1',
    'core' => ['latest' => $latest, 'php_needed' => $phpNeeded, 'checked_at' => is_object($core) && isset($core->last_checked) ? (int) $core->last_checked : 0],
    'plugins' => $plugins, 'themes' => $themes, 'admins' => $admins, 'admin_login_exists' => (bool) username_exists('admin'),
    'constants' => ['WP_DEBUG' => (bool) WP_DEBUG, 'WP_DEBUG_DISPLAY' => (bool) $const('WP_DEBUG_DISPLAY'), 'DISALLOW_FILE_EDIT' => (bool) $const('DISALLOW_FILE_EDIT'),
        'DISALLOW_FILE_MODS' => (bool) $const('DISALLOW_FILE_MODS'), 'WP_AUTO_UPDATE_CORE' => is_null($const('WP_AUTO_UPDATE_CORE')) ? null : var_export($const('WP_AUTO_UPDATE_CORE'), true),
        'AUTOMATIC_UPDATER_DISABLED' => (bool) $const('AUTOMATIC_UPDATER_DISABLED'), 'DISABLE_WP_CRON' => (bool) $const('DISABLE_WP_CRON')],
    'maintenance' => is_file(ABSPATH . '.maintenance'),
    'db' => ['name' => (string) DB_NAME, 'host' => (string) DB_HOST, 'prefix' => (string) $wpdb->base_prefix, 'tables' => (int) ($size->n ?? 0), 'bytes' => (int) ($size->bytes ?? 0)],
    'config_mode' => is_file($cfg) ? sprintf('%04o', fileperms($cfg) & 0777) : '',
], JSON_INVALID_UTF8_SUBSTITUTE) . "\n";
`

// loginScript is the one-time sign-in file: it deletes itself when it is
// first requested, works for a minute and only over HTTPS, and signs in
// only the chosen administrator. %d are the expiry (Unix time) and the user ID.
const loginScript = `<?php
// NimVor one-time sign-in link: deleted on first use, valid until %d.
@unlink(__FILE__);
// Only over HTTPS (nginx sets HTTPS=on for TLS requests), so the session
// cookie is never sent in clear.
if (time() > %d || ($_SERVER['REQUEST_METHOD'] ?? '') !== 'GET' || strtolower((string) ($_SERVER['HTTPS'] ?? '')) !== 'on') {
    http_response_code(404);
    exit;
}
require __DIR__ . '/wp-load.php';
$u = get_user_by('id', %d);
if (!$u || !user_can($u, 'manage_options')) {
    http_response_code(403);
    exit;
}
wp_set_auth_cookie($u->ID, false, true);
do_action('wp_login', $u->user_login, $u);
wp_safe_redirect(admin_url());
exit;
`
