<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Core\Config;
use WhaCP\Database\Connection;
use WhaCP\Database\Migrator;
use WhaCP\Repositories\SettingRepository;

/**
 * Self-checks for `bin/whacp doctor`: runtime, secrets on disk, database,
 * agent and scheduler. Read-only.
 */
final class Doctor
{
    public const OK = 'ok';
    public const WARN = 'warn';
    public const FAIL = 'fail';
    public const SCHEDULER_KEY = '_scheduler_last_run';

    public function __construct(
        private readonly Config $config,
        private readonly Connection $db,
        private readonly Migrator $migrator,
        private readonly AgentClient $agent,
        private readonly SettingRepository $settings,
    ) {
    }

    /** @return list<array{status: string, check: string, detail: string}> */
    public function run(): array
    {
        $out = [];
        $add = static function (string $status, string $check, string $detail) use (&$out): void {
            $out[] = ['status' => $status, 'check' => $check, 'detail' => $detail];
        };

        $add(PHP_VERSION_ID >= 80300 ? self::OK : self::FAIL, 'PHP version', PHP_VERSION . (PHP_VERSION_ID >= 80300 ? '' : ' — WhaCP needs PHP 8.3 or newer'));
        $missing = array_values(array_filter(['pdo_sqlite', 'sodium', 'curl', 'mbstring', 'json'], static fn (string $e): bool => !extension_loaded($e)));
        $add($missing === [] ? self::OK : self::FAIL, 'PHP extensions', $missing === [] ? 'all present' : 'missing: ' . implode(', ', $missing));
        $add($this->config->get('app.debug') ? self::WARN : self::OK, 'Debug mode', $this->config->get('app.debug') ? 'on — error pages show internals; turn it off' : 'off');
        $add($this->config->get('session.secure_cookie') ? self::OK : self::WARN, 'Secure session cookie', $this->config->get('session.secure_cookie') ? 'on' : 'off — only acceptable behind a TLS proxy on localhost');

        $key = (string) $this->config->get('security.key_file');
        if ($key !== '' && !file_exists($key)) {
            // Created on first use; losing it makes stored secrets unreadable.
            $secrets = 0;
            foreach (['servers' => 'token_enc', 'database_users' => 'password_enc', 'git_repositories' => 'webhook_secret_enc', 'backup_storages' => 'config_enc'] as $table => $col) {
                try {
                    $secrets += (int) $this->db->scalar("SELECT COUNT(*) FROM {$table} WHERE {$col} <> ''");
                } catch (\Throwable) {
                    // table not created yet
                }
            }
            $secrets += (int) $this->db->scalar("SELECT COUNT(*) FROM settings WHERE key = 'mail.password' AND value NOT IN ('\"\"', '')");
            $add($secrets > 0 ? self::FAIL : self::WARN, 'Panel encryption key', $secrets > 0
                ? $key . ' is missing, but encrypted secrets are stored — restore the key from a backup'
                : $key . ' does not exist yet (created when the first secret is stored)');
        }
        foreach ([
            'Panel encryption key' => [$key, false],
            'Agent token' => [(string) $this->config->get('agent.token_file'), false],
            'Panel database' => [(string) $this->config->get('database.path'), false],
            'Session folder' => [(string) $this->config->get('session.path'), true],
        ] as $name => [$path, $dir]) {
            if ($name === 'Panel encryption key' && $path !== '' && !file_exists($path)) {
                continue;
            }
            [$status, $detail] = self::permissions($path, $dir);
            $add($status, $name, $detail);
        }

        $pending = $this->migrator->pending();
        $add($pending === [] ? self::OK : self::FAIL, 'Database schema', $pending === [] ? 'up to date' : count($pending) . ' migration(s) pending — run bin/whacp migrate');
        try {
            $integrity = (string) $this->db->scalar('PRAGMA quick_check');
            $add($integrity === 'ok' ? self::OK : self::FAIL, 'Database integrity', $integrity);
        } catch (\Throwable $e) {
            $add(self::FAIL, 'Database integrity', $e->getMessage());
        }

        try {
            $health = $this->agent->health();
            $agentVersion = (string) ($health['data']['version'] ?? '?');
            $panel = (string) $this->config->get('app.version');
            $same = self::release($agentVersion) === self::release($panel);
            $add($same ? self::OK : self::WARN, 'Agent', 'reachable, version ' . $agentVersion . ($same ? '' : ' (panel ' . $panel . ') — upgrade both together'));
            foreach ($this->server() as [$status, $check, $detail]) {
                $add($status, $check, $detail);
            }
        } catch (AgentException $e) {
            $add(self::FAIL, 'Agent', trim($e->getMessage() . ' ' . $e->remedy()));
        }

        $last = (string) ($this->settings->all()[self::SCHEDULER_KEY] ?? '');
        $age = $last === '' ? null : time() - (int) strtotime($last);
        $add(
            $age !== null && $age < 600 ? self::OK : self::WARN,
            'Scheduler',
            $age === null ? 'has never run — enable whacp-scheduler.timer (monitoring, alerts and scheduled backups depend on it)'
                : ($age < 600 ? 'last run ' . $last : 'last run ' . $last . ' — check systemctl status whacp-scheduler.timer'),
        );

        $down = (int) $this->db->scalar("SELECT COUNT(*) FROM servers WHERE id <> 1 AND status = 'offline'");
        if ($down > 0) {
            $add(self::WARN, 'Remote servers', $down . ' offline — see Servers');
        }
        return $out;
    }

    /**
     * Checks of the managed server, through the agent: disk space, the
     * web, PHP, database and cache services, Let's Encrypt tooling and
     * backup configuration. A check whose data is unavailable is skipped.
     *
     * @return list<array{0: string, 1: string, 2: string}>
     */
    private function server(): array
    {
        $out = [];
        try {
            foreach ((array) (((array) $this->agent->call('system.metrics'))['disks'] ?? []) as $d) {
                if (!is_array($d) || !isset($d['used_percent'])) {
                    continue;
                }
                $pct = (float) $d['used_percent'];
                $out[] = [$pct >= 95 ? self::FAIL : ($pct >= 85 ? self::WARN : self::OK), 'Disk ' . (string) ($d['mount_point'] ?? '?'),
                    sprintf('%.0f%% used, %s free', $pct, self::bytes((int) ($d['free_bytes'] ?? 0)))];
            }
        } catch (AgentException) {
        }
        try {
            $groups = ['Nginx' => '/^nginx\.service$/', 'PHP-FPM' => '/php.*fpm\.service$/', 'Database server' => '/^(mariadb|mysql|mysqld)\.service$/', 'Redis' => '/^redis(-server)?\.service$/'];
            $units = (array) $this->agent->call('services.status');
            foreach ($groups as $name => $re) {
                $running = $stopped = [];
                foreach ($units as $u) {
                    if (!is_array($u) || !preg_match($re, (string) ($u['unit'] ?? '')) || ($u['load_state'] ?? '') !== 'loaded') {
                        continue;
                    }
                    if (!empty($u['running'])) {
                        $running[] = (string) $u['unit'];
                    } elseif (($u['unit_file_state'] ?? '') === 'enabled') {
                        $stopped[] = (string) $u['unit'];
                    }
                }
                if ($stopped !== []) {
                    $out[] = [self::FAIL, $name, 'enabled but not running: ' . implode(', ', $stopped)];
                } elseif ($running !== []) {
                    $out[] = [self::OK, $name, 'running: ' . implode(', ', $running)];
                } elseif ($name === 'Nginx') {
                    $out[] = [self::WARN, $name, 'not installed or not running — websites cannot be served'];
                }
            }
        } catch (AgentException) {
        }
        try {
            $certbot = false;
            foreach ((array) $this->agent->call('packages.list') as $p) {
                $certbot = $certbot || (is_array($p) && ($p['name'] ?? '') === 'certbot' && !empty($p['installed']));
            }
            $out[] = [$certbot ? self::OK : self::WARN, "Let's Encrypt (certbot)", $certbot ? 'installed' : "not installed — install certbot under Server → Packages to issue certificates"];
        } catch (AgentException) {
        }
        try {
            $schedules = (int) $this->db->scalar('SELECT COUNT(*) FROM backup_schedules');
            $last = (string) ($this->db->scalar("SELECT MAX(finished_at) FROM backups WHERE status = 'completed'") ?? '');
            $age = $last === '' ? null : time() - (int) strtotime($last);
            $out[] = [$schedules === 0 ? self::WARN : ($age !== null && $age > 8 * 86400 ? self::WARN : self::OK), 'Backups',
                ($schedules === 0 ? 'no backup schedule' : $schedules . ' schedule(s)') . ', last completed backup: ' . ($last === '' ? 'never' : $last)];
        } catch (\Throwable) {
            // older schema or no backup tables
        }
        return $out;
    }

    private static function bytes(int $b): string
    {
        foreach (['B', 'KB', 'MB', 'GB', 'TB'] as $i => $u) {
            if ($b < 1024 ** ($i + 1) || $u === 'TB') {
                return round($b / 1024 ** $i, 1) . ' ' . $u;
            }
        }
        return $b . ' B';
    }

    /** "1.0.0-rc1" → "1.0.0" */
    private static function release(string $v): string
    {
        return preg_replace('/[-+].*$/', '', ltrim($v, 'v')) ?? $v;
    }

    /** @return array{0: string, 1: string} */
    public static function permissions(string $path, bool $dir): array
    {
        clearstatcache(true, $path);
        if ($dir && $path !== '' && !file_exists($path)) {
            return [self::WARN, $path . ' does not exist yet (created on first sign-in)'];
        }
        if ($path === '' || !file_exists($path)) {
            return [self::FAIL, ($path === '' ? '(not configured)' : $path) . ' is missing'];
        }
        if (is_link($path)) {
            return [self::WARN, $path . ' is a symbolic link'];
        }
        $mode = fileperms($path) & 0o777;
        $desc = sprintf('%s (mode %04o)', $path, $mode);
        if ($mode & 0o007) {
            return [self::FAIL, $desc . ' — readable by all users; run chmod o-rwx ' . $path];
        }
        if ($mode & 0o020) {
            return [self::WARN, $desc . ' — writable by its group'];
        }
        return [self::OK, $desc];
    }
}
