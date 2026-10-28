<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Security\Crypto;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * The WordPress toolkit's switches and tools for one installation:
 * debugging, wp-cron.php takeover, password and hotlink protection,
 * vulnerability protection, administrator and database passwords, the
 * debug log, security hardening (apply and roll back measure by measure)
 * and staging (clone, sync, restore points).
 *
 * Who may use it is decided by the controller (the website must be visible
 * to the user, WEBSITES_MANAGE for changes). Copies are only made between
 * websites of the same owner, and the databases they touch are checked
 * here against the panel's own records, never taken from wp-config.php.
 */
final class WordPressToolsService
{
    public const DEBUG = [
        'WP_DEBUG' => 'Debugging (WP_DEBUG)',
        'WP_DEBUG_LOG' => 'Write errors to wp-content/debug.log (WP_DEBUG_LOG)',
        'WP_DEBUG_DISPLAY' => 'Show errors on pages (WP_DEBUG_DISPLAY)',
        'SCRIPT_DEBUG' => 'Unminified core scripts and styles (SCRIPT_DEBUG)',
        'SAVEQUERIES' => 'Record database queries (SAVEQUERIES)',
    ];

    /** Measures applied by one-click and automatic hardening. */
    public const RECOMMENDED = ['block_sensitive_files', 'block_author_scans', 'no_php_in_uploads', 'no_php_in_includes', 'block_bad_bots',
        'disable_file_edit', 'disable_pingbacks', 'disable_script_concat', 'config_permissions'];

    public function __construct(
        private readonly AgentClient $agent,
        private readonly Connection $db,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
        private readonly JobTracker $jobs,
        private readonly WebsiteRepository $sites,
        private readonly CronService $cron,
        private readonly ModSecurityService $modsec,
        private readonly DatabaseService $databases,
        private readonly Crypto $crypto,
        private readonly WordPressVulnerabilities $vulns,
        private readonly NotificationService $notifications,
    ) {
    }

    /** @return array<string, mixed> */
    private static function arr(mixed $v): array
    {
        return (array) json_decode((string) json_encode($v), true);
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @return array<string, mixed>
     */
    public static function target(array $site, array $install): array
    {
        return ['domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'], 'php_version' => $site['php_version'], 'path' => (string) $install['path']];
    }

    /**
     * The installation's URL path on its website ("/" or "/blog/").
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     */
    public static function urlPath(array $site, array $install): string
    {
        $dir = rtrim((string) $site['root'], '/') . ($install['path'] !== '' ? '/' . $install['path'] : '');
        $doc = rtrim((string) $site['document_root'], '/');
        if ($dir !== $doc && !str_starts_with($dir, $doc . '/')) {
            throw new UserFacingException('This installation is not inside the website\'s document root, so web server protection cannot be applied to it.');
        }
        $rest = trim(substr($dir, strlen($doc)), '/');
        if ($rest !== '' && !preg_match('#^[A-Za-z0-9._~-]+(/[A-Za-z0-9._~-]+)*$#', $rest)) {
            throw new UserFacingException('The installation\'s folder name has characters web server rules cannot use.');
        }
        return $rest === '' ? '/' : '/' . $rest . '/';
    }

    /** @param array<string, mixed> $site */
    private static function writable(array $site): void
    {
        if (!empty($site['account_suspended'])) {
            throw new UserFacingException('The hosting account is suspended; WordPress cannot be changed.', status: 409);
        }
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @param array<string, mixed> $extra
     * @return array<string, mixed>
     */
    private function manage(array $site, array $install, string $op, array $extra = [], int $timeout = 150): array
    {
        return self::arr($this->agent->call('wordpress.manage', self::target($site, $install) + ['op' => $op] + $extra, $timeout));
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @param array<string, mixed> $details
     */
    private function record(string $action, array $site, array $install, array $details, int $userId): void
    {
        $this->audit->record('wordpress.' . $action, 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'path' => $install['path']] + $details, $userId);
    }

    /**
     * Password and hotlink protection of the website (user names only).
     *
     * @param array<string, mixed> $site
     * @return array{password: list<array{path: string, user: string}>, hotlink: ?array<string, mixed>, wordpress: array<string, list<string>>}
     */
    public function protection(array $site): array
    {
        $res = self::arr($this->agent->call('protect.status', ['domain' => $site['domain']]));
        return ['password' => array_values((array) ($res['password'] ?? [])), 'hotlink' => is_array($res['hotlink'] ?? null) ? $res['hotlink'] : null, 'wordpress' => (array) ($res['wordpress'] ?? [])];
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @param list<string> $on
     */
    public function setDebug(array $site, array $install, array $on, int $userId): string
    {
        self::writable($site);
        $on = array_values(array_intersect(array_keys(self::DEBUG), $on));
        $this->manage($site, $install, 'debug', ['items' => $on]);
        $this->record('debug', $site, $install, ['enabled' => $on], $userId);
        $this->db->run('UPDATE wordpress_installs SET debug = ? WHERE id = ?', [(int) in_array('WP_DEBUG', $on, true), (int) $install['id']]);
        return $on === [] ? 'Debugging is off.' : 'Debugging settings saved: ' . implode(', ', $on) . '.';
    }

    /**
     * Take over wp-cron.php: WordPress's scheduled tasks run from a system
     * cron job every 5 minutes instead of on visits.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @param array<string, mixed> $actor
     */
    public function setCronTakeover(array $site, array $install, bool $on, array $actor): string
    {
        self::writable($site);
        $jobId = $install['cron_job_id'] !== null ? (int) $install['cron_job_id'] : null;
        if ($on) {
            $res = $this->manage($site, $install, 'cron_takeover_on');
            $cmd = (string) ($res['command'] ?? '');
            if (!preg_match('#^/[A-Za-z0-9/._-]+ /usr/local/lib/nimvor/wp-cli\.phar --path=/[A-Za-z0-9/._~-]+ cron event run --due-now --quiet$#', $cmd)) {
                throw new UserFacingException('The agent returned an unexpected cron command.');
            }
            if ($jobId === null || $this->cron->find($site, $jobId) === null) {
                try {
                    $this->cron->save($site, null, ['schedule' => '*/5 * * * *', 'command' => $cmd, 'description' => 'WordPress scheduled tasks (' . ($install['path'] === '' ? '/' : $install['path']) . ') — taken over from wp-cron.php', 'enabled' => true]);
                } catch (\Throwable $e) {
                    $this->manage($site, $install, 'cron_takeover_off');
                    throw $e;
                }
                $jobId = (int) $this->db->scalar('SELECT id FROM cron_jobs WHERE website_id = ? AND command = ? ORDER BY id DESC LIMIT 1', [(int) $site['id'], $cmd]);
                $this->db->run('UPDATE wordpress_installs SET cron_job_id = ? WHERE id = ?', [$jobId ?: null, (int) $install['id']]);
            }
            $this->record('cron_takeover', $site, $install, ['on' => true], (int) $actor['id']);
            return 'wp-cron.php is taken over: WordPress\'s scheduled tasks now run every 5 minutes from a cron job (listed under Cron jobs).';
        }
        $this->manage($site, $install, 'cron_takeover_off');
        if ($jobId !== null && $this->cron->find($site, $jobId) !== null) {
            $this->cron->delete($site, $jobId);
        }
        $this->db->run('UPDATE wordpress_installs SET cron_job_id = NULL WHERE id = ?', [(int) $install['id']]);
        $this->record('cron_takeover', $site, $install, ['on' => false], (int) $actor['id']);
        return 'WordPress runs its scheduled tasks on visits again (wp-cron.php).';
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     */
    public function setPasswordProtection(array $site, array $install, bool $on, string $user, string $password, int $userId): string
    {
        self::writable($site);
        $path = self::urlPath($site, $install);
        if ($on) {
            $errors = [];
            if (!preg_match('/^[A-Za-z0-9._-]{1,64}$/', $user)) {
                $errors['protect_user'] = 'Use up to 64 letters, digits, dots, dashes or underscores.';
            }
            if (strlen($password) < 8 || strlen($password) > 128 || preg_match('/[\x00\r\n]/', $password)) {
                $errors['protect_password'] = 'Use 8 to 128 characters.';
            }
            if ($errors !== []) {
                throw new ValidationException($errors);
            }
        }
        $this->agent->call('protect.password', ['domain' => $site['domain'], 'path' => $path, 'enabled' => $on, 'user' => $user, 'password' => $password, 'realm' => 'Restricted area'], 120);
        $this->record('password_protection', $site, $install, ['on' => $on, 'user' => $on ? $user : '', 'url_path' => $path], $userId);
        return $on ? 'Password protection is on: visitors to ' . $site['domain'] . rtrim($path, '/') . '/ must sign in as ' . $user . '.' : 'Password protection is off.';
    }

    /**
     * @param array<string, mixed> $site
     * @param list<string> $allowed
     */
    public function setHotlink(array $site, bool $on, array $allowed, int $userId): string
    {
        self::writable($site);
        $allowed = array_values(array_filter(array_map(static fn ($a): string => strtolower(trim((string) $a)), $allowed)));
        foreach ($allowed as $a) {
            if (!preg_match('/^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$/', $a)) {
                throw new ValidationException(['hotlink_allowed' => "\"{$a}\" is not a host name (a leading *. is allowed)."]);
            }
        }
        $this->agent->call('protect.hotlink', ['domain' => $site['domain'], 'enabled' => $on, 'allowed' => $allowed], 120);
        $this->audit->record('wordpress.hotlink', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'on' => $on, 'allowed' => $allowed], $userId);
        return $on ? 'Hotlink protection is on: images and media are only served to pages of this website' . ($allowed !== [] ? ' and ' . implode(', ', $allowed) : '') . '.' : 'Hotlink protection is off.';
    }

    /**
     * Vulnerability protection: the website's requests pass ModSecurity with
     * the OWASP Core Rule Set in blocking mode, and the installation is
     * checked against the vulnerability database every day.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @param array<string, mixed> $actor
     */
    public function setVulnerabilityProtection(array $site, array $install, bool $on, array $actor, bool $admin): string
    {
        self::writable($site);
        if ($on) {
            $st = $this->modsec->status();
            $state = (string) ($st['connector']['state'] ?? (!empty($st['module_loaded']) ? 'functional' : 'missing'));
            if ($state !== 'functional' || empty($st['active'])) {
                throw new UserFacingException('Vulnerability protection needs ModSecurity, which is not installed or not turned on on this server.', remedy: $admin ? 'Install and turn it on under Security → ModSecurity (with the OWASP Core Rule Set), then turn this on again.' : 'Ask your hosting provider to turn on ModSecurity.');
            }
            $this->modsec->setSite($site, 'on', null, null, false, (int) $actor['id']);
        } else {
            // Other installations of the website may still want it.
            $others = (int) $this->db->scalar('SELECT COUNT(*) FROM wordpress_installs WHERE website_id = ? AND id <> ? AND vuln_protection = 1', [(int) $site['id'], (int) $install['id']]);
            if ($others === 0) {
                $this->modsec->setSite($site, 'detect', null, null, false, (int) $actor['id']);
            }
        }
        $this->db->run('UPDATE wordpress_installs SET vuln_protection = ? WHERE id = ?', [(int) $on, (int) $install['id']]);
        $this->record('vulnerability_protection', $site, $install, ['on' => $on], (int) $actor['id']);
        $held = $on && !empty($this->modsec->status()['config']['detection_only']);
        return $on ? 'Vulnerability protection is on: requests to the website are filtered by ModSecurity with the OWASP Core Rule Set' . ($held ? ' (only logging until an administrator allows blocking under Security → ModSecurity)' : ' (blocking)') . ', and the installation is checked for known vulnerabilities every day.'
            : 'Vulnerability protection is off (ModSecurity only logs for this website now).';
    }

    /**
     * Checks the installation against the vulnerability database and keeps
     * the result.
     *
     * @param array<string, mixed> $install
     * @param array<string, mixed> $info
     * @return array{items: list<array<string, mixed>>, reachable: bool, checked_at: string}
     */
    public function checkVulnerabilities(array $install, array $info): array
    {
        $res = $this->vulns->check($info);
        $now = gmdate('c');
        if ($res['reachable'] || $res['items'] !== []) {
            $this->db->run('UPDATE wordpress_installs SET vulns = ?, vulns_checked_at = ? WHERE id = ?', [(string) json_encode($res['items']), $now, (int) $install['id']]);
        }
        return $res + ['checked_at' => $now];
    }

    /**
     * The last stored vulnerability check.
     *
     * @param array<string, mixed> $install
     * @return array{items: list<array<string, mixed>>, checked_at: ?string}
     */
    public static function storedVulnerabilities(array $install): array
    {
        $items = json_decode((string) ($install['vulns'] ?? ''), true);
        return ['items' => is_array($items) ? $items : [], 'checked_at' => $install['vulns_checked_at'] ?? null];
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     */
    public function adminPassword(array $site, array $install, int $adminId, string $password, string $confirm, int $userId): string
    {
        self::writable($site);
        if (strlen($password) < 8 || strlen($password) > 128 || preg_match('/[\x00\r\n]/', $password)) {
            throw new ValidationException(['admin_password' => 'Use 8 to 128 characters.']);
        }
        if (!hash_equals($password, $confirm)) {
            throw new ValidationException(['admin_password_confirm' => 'The passwords do not match.']);
        }
        if ($adminId < 1) {
            throw new ValidationException(['admin_id' => 'Choose an administrator.']);
        }
        $res = $this->manage($site, $install, 'admin_password', ['items' => [(string) $adminId], 'password' => $password]);
        $this->record('admin_password', $site, $install, ['admin_id' => $adminId], $userId);
        return 'The password of the WordPress administrator ' . ($res['login'] ?? '#' . $adminId) . ' was changed and their other sessions were signed out.';
    }

    /**
     * Changes the database password of the installation (and in its
     * wp-config.php); returns the new password to show once.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     */
    public function dbPassword(array $site, array $install, int $userId): string
    {
        self::writable($site);
        $password = Crypto::password(24);
        $res = $this->manage($site, $install, 'db_password', ['password' => $password]);
        $user = (string) ($res['user'] ?? '');
        // The panel's stored copy (when the owner chose to keep one).
        $row = $this->db->fetch("SELECT id, password_enc FROM database_users WHERE server_id = ? AND username = ? AND host = 'localhost'", [$this->server->id(), $user]);
        if ($row !== null && (string) $row['password_enc'] !== '') {
            $this->db->run('UPDATE database_users SET password_enc = ? WHERE id = ?', [$this->crypto->encrypt($password), (int) $row['id']]);
        }
        $this->record('db_password', $site, $install, ['db_user' => $user], $userId);
        return $password;
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @return array{path: string, exists: bool, lines: list<string>, size: int}
     */
    public function debugLog(array $site, array $install): array
    {
        $res = $this->manage($site, $install, 'debug_log', [], 90);
        return ['path' => (string) ($res['path'] ?? ''), 'exists' => !empty($res['exists']), 'lines' => array_map('strval', (array) ($res['lines'] ?? [])), 'size' => (int) ($res['size'] ?? 0)];
    }

    /**
     * Security measures: status, or apply / roll back some.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @param list<string> $measures
     * @return array{measures: list<array<string, mixed>>, problems: list<string>}
     */
    public function security(array $site, array $install, string $op, array $measures, int $userId): array
    {
        if ($op !== 'status') {
            self::writable($site);
        }
        $measures = array_values(array_unique(array_filter(array_map('strval', $measures), static fn (string $m): bool => preg_match('/^[a-z_]{3,40}$/', $m) === 1)));
        $params = self::target($site, $install) + ['url_path' => self::urlPath($site, $install), 'op' => $op, 'measures' => $measures];
        $res = self::arr($this->agent->call('wordpress.security', $params, 240));
        if ($op !== 'status') {
            $this->record('security_' . $op, $site, $install, ['measures' => $measures], $userId);
        }
        return ['measures' => array_values((array) ($res['measures'] ?? [])), 'problems' => array_map('strval', (array) ($res['problems'] ?? []))];
    }

    /**
     * Websites an installation can be cloned or synced to: the owner's
     * other PHP websites (and the same website).
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $actor
     * @return list<array<string, mixed>>
     */
    public function copyTargets(array $site, array $actor): array
    {
        $out = [];
        foreach ($this->sites->all($this->server->id(), $actor) as $s) {
            if ($s['kind'] === 'php' && (string) $s['php_version'] !== '' && $s['owner_id'] === $site['owner_id'] && $s['system_user'] !== '') {
                $out[] = $s;
            }
        }
        return $out;
    }

    /**
     * The panel's database record for a name, when it belongs to the owner.
     *
     * @return array<string, mixed>|null
     */
    private function ownedDatabase(string $name, mixed $ownerId): ?array
    {
        $row = $this->db->fetch('SELECT * FROM databases WHERE server_id = ? AND name = ?', [$this->server->id(), $name]);
        if ($row === null) {
            return null;
        }
        $owner = $row['owner_id'] === null ? null : (int) $row['owner_id'];
        $want = $ownerId === null ? null : (int) $ownerId;
        return $owner === $want ? $row : null;
    }

    /**
     * Clone an installation to another website (or folder) of the same
     * owner, with a new database. Returns the job ID.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @param array<string, mixed> $input target_site, target_path, staging, overwrite
     * @param array<string, mixed> $actor
     */
    public function cloneTo(array $site, array $install, array $input, array $actor): string
    {
        self::writable($site);
        $target = $this->sites->find($this->server->id(), (int) ($input['target_site'] ?? 0), $actor);
        if ($target === null || $target['owner_id'] !== $site['owner_id'] || $target['kind'] !== 'php' || (string) $target['php_version'] === '') {
            throw new ValidationException(['target_site' => 'Choose one of the owner\'s PHP websites.']);
        }
        self::writable($target);
        $docRel = trim(substr((string) $target['document_root'], strlen((string) $target['root'])), '/');
        $sub = trim((string) ($input['target_path'] ?? ''), '/');
        if ($sub !== '' && !preg_match('#^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$#', $sub)) {
            throw new ValidationException(['target_path' => 'Use a folder name of letters, digits, dots, dashes and underscores.']);
        }
        $path = trim($docRel . ($sub !== '' ? '/' . $sub : ''), '/');
        $srcDb = $this->ownedDatabase((string) $install['db_name'], $site['owner_id']);
        if ($srcDb === null) {
            throw new UserFacingException('The installation\'s database ' . $install['db_name'] . ' is not one of the owner\'s databases in the panel, so it cannot be copied.', remedy: 'Scan the website again, or assign the database to the owner under Databases.');
        }
        $name = 'wp' . substr(bin2hex(random_bytes(4)), 0, 6);
        $created = $this->databases->create(['name' => $name, 'create_user' => true, 'username' => $name, 'website_id' => (int) $target['id'], 'store_password' => false], $actor);
        $dbName = (string) $this->db->scalar('SELECT name FROM databases WHERE id = ?', [(int) $created['database_id']]);
        $url = rtrim(WordPressService::baseUrl($target, ['path' => $path]) ?? '', '/');
        $params = ['source' => self::target($site, $install), 'target' => self::target($target, ['path' => $path]), 'mode' => 'clone',
            'source_db' => (string) $install['db_name'], 'db' => ['name' => $dbName, 'user' => (string) $created['user'], 'password' => (string) $created['password'], 'host' => 'localhost'],
            'url' => $url, 'staging' => filter_var($input['staging'] ?? true, FILTER_VALIDATE_BOOL), 'overwrite' => filter_var($input['overwrite'] ?? false, FILTER_VALIDATE_BOOL)];
        try {
            $res = self::arr($this->agent->call('wordpress.copy', $params, 120));
        } catch (AgentException $e) {
            try {
                $this->databases->drop($this->db->fetch('SELECT * FROM databases WHERE id = ?', [(int) $created['database_id']]) ?? [], $dbName);
            } catch (\Throwable) {
            }
            throw $e;
        }
        $job = (string) $res['job_id'];
        $this->db->run(
            'INSERT INTO wordpress_installs (server_id, website_id, path, version, db_name, latest, scanned_at, cloned_from) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
             ON CONFLICT (website_id, path) DO UPDATE SET db_name = excluded.db_name, cloned_from = excluded.cloned_from',
            [$this->server->id(), (int) $target['id'], $path, (string) $install['version'], $dbName, (string) $install['latest'], gmdate('c'), (int) $install['id']]
        );
        $this->jobs->track($job, 'Clone WordPress: ' . $site['domain'] . ' → ' . $url, 'website', (string) $target['id'], (int) $actor['id']);
        $this->record('clone', $site, $install, ['target' => $target['domain'], 'target_path' => $path, 'database' => $dbName], (int) $actor['id']);
        return $job;
    }

    /**
     * Copy files and/or the database from one installation to another of
     * the same owner (the target's wp-config.php is kept). Returns the job.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install the source
     * @param array<string, mixed> $input target (installation ID), files, database
     * @param array<string, mixed> $actor
     */
    public function sync(array $site, array $install, array $input, array $actor): string
    {
        self::writable($site);
        $dest = $this->db->fetch('SELECT * FROM wordpress_installs WHERE id = ? AND server_id = ?', [(int) ($input['target'] ?? 0), $this->server->id()]);
        $target = $dest === null ? null : $this->sites->find($this->server->id(), (int) $dest['website_id'], $actor);
        if ($dest === null || $target === null || $target['owner_id'] !== $site['owner_id'] || (int) $dest['id'] === (int) $install['id']) {
            throw new ValidationException(['target' => 'Choose another WordPress installation of the same owner.']);
        }
        self::writable($target);
        $files = filter_var($input['files'] ?? false, FILTER_VALIDATE_BOOL);
        $database = filter_var($input['database'] ?? false, FILTER_VALIDATE_BOOL);
        if (!$files && !$database) {
            throw new ValidationException(['files' => 'Choose files, the database or both.']);
        }
        if ($database) {
            foreach ([[(string) $install['db_name'], $site], [(string) $dest['db_name'], $target]] as [$name, $s]) {
                if ($this->ownedDatabase($name, $s['owner_id']) === null) {
                    throw new UserFacingException('The database ' . $name . ' is not one of the owner\'s databases in the panel, so it cannot be synced.', remedy: 'Scan both websites again, or assign the database to the owner under Databases.');
                }
            }
        }
        $url = rtrim(WordPressService::baseUrl($target, $dest) ?? '', '/');
        if ($url === '') {
            throw new UserFacingException('The target installation is not inside its website\'s document root.');
        }
        $params = ['source' => self::target($site, $install), 'target' => self::target($target, $dest), 'mode' => 'sync', 'files' => $files, 'database' => $database,
            'source_db' => (string) $install['db_name'], 'target_db' => (string) $dest['db_name'], 'url' => $url];
        $res = self::arr($this->agent->call('wordpress.copy', $params, 120));
        $job = (string) $res['job_id'];
        $what = $files && $database ? 'files and database' : ($files ? 'files' : 'database');
        $this->jobs->track($job, 'Sync WordPress ' . $what . ': ' . $site['domain'] . ' → ' . $url, 'website', (string) $target['id'], (int) $actor['id']);
        $this->record('sync', $site, $install, ['target' => $target['domain'], 'target_path' => $dest['path'], 'files' => $files, 'database' => $database], (int) $actor['id']);
        return $job;
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @return list<array<string, mixed>>
     */
    public function restorePoints(array $site, array $install): array
    {
        $res = self::arr($this->agent->call('wordpress.restore_points', ['domain' => $site['domain']]));
        return array_values(array_filter((array) ($res['restore_points'] ?? []), static fn ($p): bool => is_array($p) && (string) ($p['path'] ?? '') === (string) $install['path']));
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $install
     * @param array<string, mixed> $actor
     */
    public function restore(array $site, array $install, string $point, array $actor): string
    {
        self::writable($site);
        if (!preg_match('/^\d{8}T\d{6}Z$/', $point)) {
            throw new ValidationException(['restore_point' => 'Choose a restore point.']);
        }
        if ($this->ownedDatabase((string) $install['db_name'], $site['owner_id']) === null) {
            throw new UserFacingException('The installation\'s database is not one of the owner\'s databases in the panel.');
        }
        $params = ['source' => self::target($site, $install), 'target' => self::target($site, $install), 'mode' => 'restore', 'restore_point' => $point, 'target_db' => (string) $install['db_name']];
        $res = self::arr($this->agent->call('wordpress.copy', $params, 120));
        $job = (string) $res['job_id'];
        $this->jobs->track($job, 'Restore WordPress: ' . $site['domain'] . ' (' . $point . ')', 'website', (string) $site['id'], (int) $actor['id']);
        $this->record('restore', $site, $install, ['restore_point' => $point], (int) $actor['id']);
        return $job;
    }

    /**
     * Daily check of installations with vulnerability protection: newly
     * found vulnerabilities are notified to the website's owner.
     *
     * @return list<string>
     */
    public function dailyVulnerabilityCheck(): array
    {
        $out = [];
        $due = $this->db->fetchAll(
            "SELECT i.*, w.domain, w.system_user, w.root, w.php_version, w.owner_id, " . \NimVor\Repositories\UserRepository::SUSPENDED . " AS account_suspended FROM wordpress_installs i JOIN websites w ON w.id = i.website_id LEFT JOIN users u ON u.id = w.owner_id
             WHERE i.server_id = ? AND i.vuln_protection = 1 AND (i.vulns_checked_at IS NULL OR i.vulns_checked_at < ?) ORDER BY i.vulns_checked_at LIMIT 20",
            [$this->server->id(), gmdate('c', time() - 20 * 3600)]
        );
        foreach ($due as $row) {
            $where = $row['domain'] . ($row['path'] !== '' ? '/' . $row['path'] : '');
            try {
                $info = self::arr($this->agent->call('wordpress.info', self::target($row, $row), 120));
            } catch (AgentException $e) {
                $out[] = 'WordPress ' . $where . ': not checked (' . $e->getMessage() . ')';
                continue;
            }
            $before = array_column(self::storedVulnerabilities($row)['items'], 'name');
            $res = $this->checkVulnerabilities($row, $info);
            $new = array_values(array_filter($res['items'], static fn (array $v): bool => !in_array($v['name'], $before, true)));
            if ($new !== []) {
                $this->notifications->notify('wp-vulns:' . $row['id'] . ':' . sha1(implode('|', array_column($new, 'name'))), 'wordpress', 'warning',
                    count($new) . ' new vulnerabilit' . (count($new) === 1 ? 'y' : 'ies') . ' in WordPress on ' . $where, [
                        'website_id' => (int) $row['website_id'], 'owner_id' => $row['owner_id'] === null ? null : (int) $row['owner_id'],
                        'link' => '/sites/' . (int) $row['website_id'] . '/wordpress/' . (int) $row['id'],
                        'body' => implode("\n", array_map(static fn (array $v): string => $v['title'] . ': ' . $v['name'] . ($v['fixed_in'] !== '' ? ' (fixed in ' . $v['fixed_in'] . ')' : ''), array_slice($new, 0, 10))),
                    ]);
            }
            $out[] = 'WordPress ' . $where . ': ' . count($res['items']) . ' known vulnerabilit' . (count($res['items']) === 1 ? 'y' : 'ies') . ($res['reachable'] ? '' : ' (database partly unreachable)');
        }
        return $out;
    }

    /**
     * Automatic hardening: WordPress installed with the app installer gets
     * the recommended measures once its installation job has succeeded.
     *
     * @return list<string>
     */
    public function autoHarden(bool $enabled): array
    {
        $out = [];
        $rows = $this->db->fetchAll(
            "SELECT a.*, j.status AS job_status FROM app_installs a JOIN websites w ON w.id = a.website_id
             JOIN agent_jobs j ON j.job_id = a.job_id AND j.server_id = w.server_id
             WHERE w.server_id = ? AND a.app = 'wordpress' AND a.hardened = 0 AND j.status IN ('succeeded', 'failed', 'unknown') LIMIT 10",
            [$this->server->id()]
        );
        foreach ($rows as $a) {
            $this->db->run('UPDATE app_installs SET hardened = 1 WHERE id = ?', [(int) $a['id']]);
            $site = $this->sites->find($this->server->id(), (int) $a['website_id'], null);
            if (!$enabled || $a['job_status'] !== 'succeeded' || $site === null) {
                continue;
            }
            $dbName = (string) $this->db->scalar('SELECT name FROM databases WHERE id = ?', [(int) $a['database_id']]);
            $this->db->run(
                'INSERT INTO wordpress_installs (server_id, website_id, path, db_name, scanned_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT (website_id, path) DO UPDATE SET db_name = excluded.db_name',
                [$this->server->id(), (int) $site['id'], (string) $a['path'], $dbName, gmdate('c')]
            );
            $install = $this->db->fetch('SELECT * FROM wordpress_installs WHERE website_id = ? AND path = ?', [(int) $site['id'], (string) $a['path']]);
            try {
                $res = $this->security($site, (array) $install, 'apply', self::RECOMMENDED, (int) ($a['user_id'] ?? 0));
                $out[] = 'WordPress ' . $site['domain'] . '/' . $a['path'] . ': hardened' . ($res['problems'] !== [] ? ' (' . implode('; ', $res['problems']) . ')' : '');
            } catch (\Throwable $e) {
                $out[] = 'WordPress ' . $site['domain'] . '/' . $a['path'] . ': not hardened: ' . $e->getMessage();
            }
        }
        return $out;
    }
}
