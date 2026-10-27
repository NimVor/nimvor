<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Database\Connection;
use NimVor\Hosting\Scope;
use NimVor\Support\UserFacingException;

/**
 * WordPress toolkit. The agent finds installations in websites (reading
 * files only) and manages them with WP-CLI as the website account, with the
 * website's plugins and themes not loaded. The panel keeps what the scans
 * report, decides who may see and change which installation (the website's
 * visibility and WEBSITES_MANAGE; the "wordpress" feature for hosting
 * plans), and turns the overview into security checks.
 */
final class WordPressService
{
    /** Operations the panel offers: op => [label, runs as a job]. */
    public const OPS = [
        'core_update' => ['Update WordPress', true],
        'plugin_update' => ['Update plugins', true],
        'theme_update' => ['Update themes', true],
        'plugin_activate' => ['Activate plugin', false],
        'plugin_deactivate' => ['Deactivate plugin', false],
        'plugin_auto_on' => ['Turn automatic updates on', false],
        'plugin_auto_off' => ['Turn automatic updates off', false],
        'theme_activate' => ['Activate theme', false],
        'theme_auto_on' => ['Turn automatic updates on', false],
        'theme_auto_off' => ['Turn automatic updates off', false],
        'maintenance_on' => ['Turn maintenance mode on', false],
        'maintenance_off' => ['Turn maintenance mode off', false],
        'disable_file_edit' => ['Turn off the file editor', false],
        'debug_off' => ['Turn debugging off', false],
        'protect_config' => ['Protect wp-config.php', false],
        'core_auto_update' => ['Automatic WordPress updates', false],
        'verify_core' => ['Verify core files', false],
        'search_engines_on' => ['Let search engines index the site', false],
        'search_engines_off' => ['Ask search engines not to index the site', false],
    ];

    public function __construct(
        private readonly AgentClient $agent,
        private readonly Connection $db,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
        private readonly JobTracker $jobs,
    ) {
    }

    /** @return array<string, mixed> */
    private static function arr(mixed $v): array
    {
        return (array) json_decode((string) json_encode($v), true);
    }

    /**
     * Installations the user may see, with their website.
     *
     * @param array<string, mixed> $user
     * @return list<array<string, mixed>>
     */
    public function installs(array $user, ?int $websiteId = null, string $filter = ''): array
    {
        [$cond, $params] = Scope::condition($user, 'w.owner_id');
        $params['srv'] = $this->server->id();
        $sql = "SELECT i.*, w.domain, w.https, w.php_version, w.system_user, w.document_root, w.root, u.username AS owner_name
                FROM wordpress_installs i JOIN websites w ON w.id = i.website_id LEFT JOIN users u ON u.id = w.owner_id
                WHERE i.server_id = :srv AND {$cond}";
        if ($websiteId !== null) {
            $sql .= ' AND i.website_id = :wid';
            $params['wid'] = $websiteId;
        }
        $rows = $this->db->fetchAll($sql . ' ORDER BY w.domain, i.path', $params);
        foreach ($rows as &$r) {
            $r['outdated'] = self::outdated($r);
            $r['url'] = self::baseUrl($r, $r);
        }
        unset($r);
        if ($filter === 'outdated') {
            $rows = array_values(array_filter($rows, static fn (array $r): bool => $r['outdated'] || (int) $r['plugin_updates'] + (int) $r['theme_updates'] > 0));
        } elseif ($filter === 'issues') {
            $rows = array_values(array_filter($rows, static fn (array $r): bool => (int) $r['issues'] > 0));
        }
        return $rows;
    }

    /** @param array<string, mixed> $i */
    public static function outdated(array $i): bool
    {
        return (string) $i['latest'] !== '' && (string) $i['version'] !== '' && version_compare((string) $i['version'], (string) $i['latest'], '<');
    }

    /**
     * One installation of a website.
     *
     * @param array<string, mixed> $site
     * @return array<string, mixed>
     */
    public function install(array $site, int $id): array
    {
        $row = $this->db->fetch('SELECT * FROM wordpress_installs WHERE id = ? AND website_id = ? AND server_id = ?', [$id, (int) $site['id'], $this->server->id()]);
        if ($row === null) {
            throw new UserFacingException('That WordPress installation was not found; scan the website again.', status: 404);
        }
        $row['outdated'] = self::outdated($row);
        $row['url'] = self::baseUrl($site, $row);
        return $row;
    }

    /**
     * The installation's address, from the website's own domain and
     * document root (never from what WordPress says about itself).
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     */
    public static function baseUrl(array $site, array $install): ?string
    {
        $dir = rtrim((string) $site['root'], '/') . ($install['path'] !== '' ? '/' . $install['path'] : '');
        $doc = rtrim((string) $site['document_root'], '/');
        if ($dir !== $doc && !str_starts_with($dir, $doc . '/')) {
            return null;
        }
        $rest = substr($dir, strlen($doc));
        return ($site['https'] ? 'https://' : 'http://') . $site['domain'] . implode('/', array_map('rawurlencode', explode('/', $rest)));
    }

    /**
     * Scans websites: each website folder once (a website inside another's
     * folder is scanned with it), each installation recorded under the
     * website whose document root holds it.
     *
     * @param list<array<string, mixed>> $sites websites the actor may see
     * @return array{found: int, errors: list<string>, scanned: int}
     */
    public function scan(array $sites, int $userId): array
    {
        $sites = array_values(array_filter($sites, static fn (array $s): bool => $s['kind'] === 'php' && (string) $s['php_version'] !== ''));
        if ($sites === []) {
            throw new UserFacingException('There is no PHP website to scan.');
        }
        $within = static fn (string $p, string $base): bool => $p === $base || str_starts_with($p, rtrim($base, '/') . '/');
        $tops = array_values(array_filter($sites, static function (array $s) use ($sites, $within): bool {
            foreach ($sites as $o) {
                if ((int) $o['id'] !== (int) $s['id'] && (string) $o['root'] !== (string) $s['root'] && $within((string) $s['root'], (string) $o['root'])) {
                    return false;
                }
            }
            return true;
        }));
        $params = array_map(static fn (array $s): array => ['domain' => $s['domain'], 'user' => $s['system_user'], 'root' => $s['root'], 'php_version' => $s['php_version']], $tops);
        $res = self::arr($this->agent->call('wordpress.scan', ['sites' => $params], 290));
        $byDomain = [];
        foreach ($tops as $s) {
            $byDomain[strtolower((string) $s['domain'])] = $s;
        }
        $failed = [];
        foreach ((array) ($res['errors'] ?? []) as $e) {
            $failed[strtolower((string) ($e['domain'] ?? ''))] = (string) ($e['error'] ?? '');
        }
        // Websites whose folders were scanned: their old rows are replaced.
        $covered = [];
        foreach ($sites as $s) {
            foreach ($tops as $t) {
                if (!isset($failed[strtolower((string) $t['domain'])]) && $within((string) $s['root'], (string) $t['root'])) {
                    $covered[(int) $s['id']] = $s;
                }
            }
        }
        $now = gmdate('c');
        $latest = preg_match('/^[0-9][0-9A-Za-z.+-]{0,30}$/', (string) ($res['latest'] ?? '')) ? (string) $res['latest'] : '';
        $keep = [];
        $found = 0;
        foreach ((array) ($res['installs'] ?? []) as $i) {
            $top = $byDomain[strtolower((string) ($i['domain'] ?? ''))] ?? null;
            if ($top === null) {
                continue;
            }
            $dir = rtrim((string) $top['root'], '/') . ((string) $i['path'] !== '' ? '/' . $i['path'] : '');
            // The website (of the same account) whose document root holds it.
            $best = null;
            $bestLen = -1;
            foreach ($covered as $s) {
                if ($s['system_user'] !== $top['system_user']) {
                    continue;
                }
                foreach ([(string) $s['document_root'] => 1000, (string) $s['root'] => 0] as $base => $bonus) {
                    if ($within($dir, $base) && strlen($base) + $bonus > $bestLen) {
                        $best = $s;
                        $bestLen = strlen($base) + $bonus;
                    }
                }
            }
            if ($best === null) {
                continue;
            }
            $path = ltrim(substr($dir, strlen(rtrim((string) $best['root'], '/'))), '/');
            $this->db->run(
                'INSERT INTO wordpress_installs (server_id, website_id, path, version, db_name, db_host, table_prefix, debug, file_edit_disabled, config_mode, maintenance, latest, scanned_at)
                 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                 ON CONFLICT (website_id, path) DO UPDATE SET version = excluded.version, db_name = excluded.db_name, db_host = excluded.db_host, table_prefix = excluded.table_prefix,
                   debug = excluded.debug, file_edit_disabled = excluded.file_edit_disabled, config_mode = excluded.config_mode, maintenance = excluded.maintenance,
                   latest = CASE WHEN excluded.latest <> \'\' THEN excluded.latest ELSE wordpress_installs.latest END, scanned_at = excluded.scanned_at',
                [$this->server->id(), (int) $best['id'], $path, (string) $i['version'], (string) $i['db_name'], (string) $i['db_host'], (string) $i['table_prefix'],
                    (int) !empty($i['debug']), (int) !empty($i['file_edit_disabled']), (string) $i['config_mode'], (int) !empty($i['maintenance']), $latest, $now]
            );
            $keep[(int) $best['id']][] = $path;
            $found++;
        }
        foreach (array_keys($covered) as $wid) {
            $paths = $keep[$wid] ?? [];
            $in = $paths === [] ? '' : ' AND path NOT IN (' . implode(',', array_fill(0, count($paths), '?')) . ')';
            $this->db->run('DELETE FROM wordpress_installs WHERE website_id = ?' . $in, array_merge([$wid], $paths));
        }
        $errors = [];
        foreach ($failed as $domain => $msg) {
            $errors[] = $domain . ': ' . $msg;
        }
        $this->audit->record('wordpress.scan', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['websites' => count($tops), 'found' => $found], $userId);
        return ['found' => $found, 'errors' => $errors, 'scanned' => count($tops)];
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @return array<string, mixed>
     */
    private static function target(array $site, array $install): array
    {
        return ['domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'], 'php_version' => $site['php_version'], 'path' => (string) $install['path']];
    }

    /**
     * The installation's overview (from WordPress, as the website account),
     * with the security checks; the stored summary is refreshed.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @return array{info: array<string, mixed>, checks: list<array<string, mixed>>}
     */
    public function info(array $site, array $install, bool $force = false): array
    {
        $info = self::arr($this->agent->call('wordpress.info', self::target($site, $install) + ['force' => $force], $force ? 180 : 120));
        $checks = self::checks($info, $site);
        $pluginUpdates = count(array_filter((array) ($info['plugins'] ?? []), static fn ($p): bool => ($p['update'] ?? '') !== ''));
        $themeUpdates = count(array_filter((array) ($info['themes'] ?? []), static fn ($t): bool => ($t['update'] ?? '') !== ''));
        $latest = (string) ($info['core']['latest'] ?? '');
        $this->db->run(
            'UPDATE wordpress_installs SET version = ?, siteurl = ?, latest = CASE WHEN ? <> \'\' THEN ? ELSE latest END, plugin_updates = ?, theme_updates = ?, issues = ?, maintenance = ?, debug = ?, file_edit_disabled = ?, config_mode = ?, checked_at = ? WHERE id = ?',
            [(string) ($info['version'] ?? ''), (string) ($info['siteurl'] ?? ''), $latest, $latest, $pluginUpdates, $themeUpdates,
                count(array_filter($checks, static fn (array $c): bool => $c['level'] !== 'info')), (int) !empty($info['maintenance']),
                (int) !empty($info['constants']['WP_DEBUG']), (int) !empty($info['constants']['DISALLOW_FILE_EDIT']), (string) ($info['config_mode'] ?? ''), gmdate('c'), (int) $install['id']]
        );
        return ['info' => $info, 'checks' => $checks];
    }

    /**
     * Security and health checks: [level (danger|warning|info), title, detail, fix op or null].
     *
     * @param array<string, mixed> $info
     * @param array<string, mixed> $site
     * @return list<array{level: string, title: string, detail: string, fix: ?string}>
     */
    public static function checks(array $info, array $site): array
    {
        $c = [];
        $add = static function (string $level, string $title, string $detail, ?string $fix = null) use (&$c): void {
            $c[] = ['level' => $level, 'title' => $title, 'detail' => $detail, 'fix' => $fix];
        };
        $version = (string) ($info['version'] ?? '');
        $latest = (string) ($info['core']['latest'] ?? '');
        $const = (array) ($info['constants'] ?? []);
        if ($latest !== '' && version_compare($version, $latest, '<')) {
            $add('danger', "WordPress {$latest} is available", "This site runs {$version}. Updates close security holes; back the website up first.", 'core_update');
        }
        $needed = (string) ($info['core']['php_needed'] ?? '');
        if ($needed !== '' && version_compare((string) $site['php_version'], $needed, '<')) {
            $add('danger', "The newest WordPress needs PHP {$needed}", 'The website uses PHP ' . $site['php_version'] . '; choose a newer PHP version under PHP & settings.');
        } elseif (version_compare((string) $site['php_version'], '8.1', '<')) {
            $add('warning', 'PHP ' . $site['php_version'] . ' no longer gets security fixes', 'Choose PHP 8.1 or newer under PHP & settings (check that the plugins support it).');
        }
        $plugins = array_filter((array) ($info['plugins'] ?? []), static fn ($p): bool => ($p['update'] ?? '') !== '');
        if ($plugins !== []) {
            $add('warning', count($plugins) . ' plugin update(s) available', implode(', ', array_map(static fn ($p): string => (string) $p['title'], $plugins)), 'plugin_update');
        }
        $themes = array_filter((array) ($info['themes'] ?? []), static fn ($t): bool => ($t['update'] ?? '') !== '');
        if ($themes !== []) {
            $add('warning', count($themes) . ' theme update(s) available', implode(', ', array_map(static fn ($t): string => (string) $t['title'], $themes)), 'theme_update');
        }
        if (!empty($const['WP_DEBUG'])) {
            $add('warning', 'Debugging is on', 'WP_DEBUG can show errors, paths and queries to visitors.', 'debug_off');
        }
        if (empty($const['DISALLOW_FILE_EDIT']) && empty($const['DISALLOW_FILE_MODS'])) {
            $add('warning', 'The theme and plugin file editor is on', 'Anyone who gets into an administrator account can change PHP code from the dashboard.', 'disable_file_edit');
        }
        $mode = (string) ($info['config_mode'] ?? '');
        if ($mode !== '' && (octdec($mode) & 0o027) !== 0) {
            $add('warning', 'wp-config.php is readable by others (' . $mode . ')', 'It holds the database password; only the website account needs to read it.', 'protect_config');
        }
        if (!empty($info['admin_login_exists'])) {
            $add('warning', 'A user is called "admin"', 'It is the first name attackers try. Create another administrator in WordPress and delete this one.');
        }
        $siteurl = (string) ($info['siteurl'] ?? '');
        if ($site['https'] && str_starts_with($siteurl, 'http://')) {
            $add('warning', 'WordPress uses http:// although the website has HTTPS', 'Change the WordPress and site address to https:// under Settings → General in WordPress.');
        } elseif (!$site['https']) {
            $add('warning', 'The website has no HTTPS', 'Sign-ins and cookies travel unencrypted. Issue a certificate under SSL / HTTPS.');
        }
        $auto = $const['WP_AUTO_UPDATE_CORE'] ?? null;
        if (!empty($const['AUTOMATIC_UPDATER_DISABLED']) || $auto === 'false') {
            $add('warning', 'Automatic WordPress updates are off', 'Security releases are not installed by themselves.');
        }
        $inactive = array_filter((array) ($info['plugins'] ?? []), static fn ($p): bool => empty($p['active']));
        if ($inactive !== []) {
            $add('info', count($inactive) . ' inactive plugin(s)', 'Plugins that are not used are still reachable on the web; delete them in WordPress if you do not need them.');
        }
        if (!empty($info['maintenance'])) {
            $add('info', 'Maintenance mode is on', 'Visitors see "Briefly unavailable for scheduled maintenance".', 'maintenance_off');
        }
        if (empty($info['search_engines'])) {
            $add('info', 'Search engines are asked not to index this site', 'Settings → Reading → Search engine visibility.', 'search_engines_on');
        }
        if ($latest === '') {
            // WordPress records the time of a check before asking, so only
            // a missing answer tells that WordPress.org was not reached.
            $add('warning', 'Updates could not be checked', 'WordPress.org could not be reached from this server, so available updates are not known. Use "Check for updates" to try again.');
        }
        return $c;
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @param list<string> $items plugin or theme names
     * @param array<string, mixed> $actor
     * @return array{message: string, job?: string, files?: list<string>}
     */
    public function manage(array $site, array $install, string $op, array $items, string $value, array $actor): array
    {
        if (!isset(self::OPS[$op])) {
            throw new UserFacingException('Unknown operation.');
        }
        if (!empty($site['account_suspended'])) {
            throw new UserFacingException('The hosting account is suspended; WordPress cannot be changed.', status: 409);
        }
        $items = array_values(array_unique(array_filter(array_map('strval', $items), static fn (string $s): bool => preg_match('/^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$/', $s) === 1)));
        $params = self::target($site, $install) + ['op' => $op, 'items' => $items];
        if ($op === 'core_auto_update') {
            if (!in_array($value, ['minor', 'major', 'off'], true)) {
                throw new UserFacingException('Choose minor, all or no automatic updates.');
            }
            $params['value'] = $value;
        }
        $label = self::OPS[$op][0];
        $res = self::arr($this->agent->call('wordpress.manage', $params, $op === 'verify_core' ? 330 : 150));
        $where = $site['domain'] . ($install['path'] !== '' ? ' (' . $install['path'] . ')' : '');
        $this->audit->record('wordpress.' . $op, 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'path' => $install['path'], 'items' => $items, 'value' => $value], (int) $actor['id']);
        if (isset($res['job_id'])) {
            $job = (string) $res['job_id'];
            $this->jobs->track($job, $label . ': ' . $where, 'website', (string) $site['id'], (int) $actor['id']);
            return ['message' => $label . ' for ' . $where . ' started; you will be notified when it finishes.', 'job' => $job];
        }
        if ($op === 'verify_core') {
            $files = array_map('strval', (array) ($res['files'] ?? []));
            return ['message' => !empty($res['ok']) ? 'All WordPress core files match the official release.' : count($files) . ' core file(s) differ from the official release.', 'files' => $files];
        }
        return ['message' => $label . ': done for ' . $where . '.'];
    }

    /**
     * A one-time sign-in address for an administrator of the installation.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @param array<string, mixed> $actor
     */
    public function loginUrl(array $site, array $install, int $adminId, array $actor): string
    {
        if (!empty($site['account_suspended'])) {
            throw new UserFacingException('The hosting account is suspended.', status: 409);
        }
        $base = self::baseUrl($site, $install);
        if ($base === null) {
            throw new UserFacingException('This installation is not inside the website\'s document root, so it cannot be opened from here.');
        }
        $res = self::arr($this->agent->call('wordpress.login', self::target($site, $install) + ['admin_id' => $adminId], 90));
        $file = (string) ($res['file'] ?? '');
        if (!preg_match('/^nimvor-login-[a-f0-9]{32}\.php$/', $file)) {
            throw new UserFacingException('The sign-in link could not be created.');
        }
        $this->audit->record('wordpress.login', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'path' => $install['path'], 'admin_id' => $adminId], (int) $actor['id']);
        return rtrim($base, '/') . '/' . $file;
    }

    /**
     * The panel's ID of a database by name, within a visibility condition.
     *
     * @param array<string, mixed> $params
     */
    public function databaseId(string $name, string $cond, array $params): ?int
    {
        $id = $this->db->scalar("SELECT id FROM databases WHERE server_id = :srv AND name = :name AND {$cond}", ['srv' => $this->server->id(), 'name' => $name] + $params);
        return $id === null || $id === false ? null : (int) $id;
    }
}
