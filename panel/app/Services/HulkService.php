<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Security\Countries;
use WhaCP\Support\Logger;

/**
 * Brute force protection (Security → cPHulk Brute Force Protection).
 *
 * Failed logins come from the panel itself and, through the agent, from the
 * logs of the SSH server, Dovecot (IMAP/POP3), Postfix (SMTP AUTH), pure-ftpd
 * and webmail. Too many failures lock the user name (WhaCP sign-ins) or
 * block the address: WhaCP sign-ins, and at the server (nftables) either
 * the login services' ports or, at the firewall level, every port. Blocks
 * end on their own. Blacklisted countries cannot use the login services or
 * sign in to WhaCP; whitelisted countries and addresses are never blocked.
 * Commands run as the panel's own account (never root), with a 15-second
 * limit and every variable shell-quoted.
 */
final class HulkService
{
    public const COMMAND_TIMEOUT = 15;
    public const VARIABLES = [
        'exptime' => 'The Unix time when brute force protection will release the block',
        'max_allowed_failures' => 'Maximum allowed failures to trigger this type (excessive or non-excessive failures)',
        'current_failures' => 'Number of current failures',
        'excessive_failures' => '0 (not an excessive login failure) or 1 (an excessive login failure)',
        'reason' => 'The reason for the block',
        'remote_ip' => 'The blocked IP address',
        'authservice' => 'The last service to request authentication (for example, webmaild)',
        'user' => 'The last username to request authentication',
        'logintime' => 'The time of the request',
        'ip_version' => 'The IP version (4 or 6)',
    ];

    /** @var callable(): int */
    private $clock;

    public function __construct(
        private readonly Connection $db,
        private readonly SettingsService $settings,
        private readonly AgentClient $agent,
        private readonly GeoIpService $geo,
        private readonly NotificationService $notifications,
        private readonly AuditLogger $audit,
        private readonly Logger $log,
        private readonly ServerContext $server,
        ?callable $clock = null,
    ) {
        $this->clock = $clock ?? time(...);
    }

    public function enabled(): bool
    {
        return $this->settings->bool('hulk_enabled');
    }

    private function now(): int
    {
        return ($this->clock)();
    }

    // ---- Addresses and countries ------------------------------------------

    /** @return list<string> whitelisted addresses and networks */
    public function whitelist(): array
    {
        return array_values(array_filter(preg_split('/[\s,]+/', $this->settings->string('hulk_whitelist')) ?: [], static fn (string $s): bool => $s !== ''));
    }

    /** Add an address to the whitelist (when it is not covered yet). */
    public function addToWhitelist(string $ip): void
    {
        if (!self::validEntry($ip) || $this->whitelisted($ip)) {
            return;
        }
        $list = $this->whitelist();
        $list[] = $ip;
        $this->settings->update(['hulk_whitelist' => implode(', ', $list)], null);
        $this->audit->record('hulk.whitelist_add', 'server', 'hulk', AuditLogger::SUCCESS, ['ip' => $ip]);
        if ($this->enabled()) {
            $this->syncQuietly();
        }
    }

    public static function validEntry(string $s): bool
    {
        [$addr, $len] = array_pad(explode('/', $s, 2), 2, null);
        $b = @inet_pton((string) $addr);
        if ($b === false) {
            return false;
        }
        return $len === null || (ctype_digit($len) && (int) $len <= strlen($b) * 8);
    }

    public static function inNetwork(string $ip, string $net): bool
    {
        [$addr, $len] = array_pad(explode('/', $net, 2), 2, null);
        $a = @inet_pton($ip);
        $n = @inet_pton((string) $addr);
        if ($a === false || $n === false || strlen($a) !== strlen($n)) {
            return false;
        }
        $bits = $len === null ? strlen($n) * 8 : (int) $len;
        $bytes = intdiv($bits, 8);
        if (substr($a, 0, $bytes) !== substr($n, 0, $bytes)) {
            return false;
        }
        $rest = $bits % 8;
        if ($rest === 0) {
            return true;
        }
        $mask = (0xff << (8 - $rest)) & 0xff;
        return (ord($a[$bytes]) & $mask) === (ord($n[$bytes]) & $mask);
    }

    public function whitelisted(string $ip): bool
    {
        foreach ($this->whitelist() as $w) {
            if (self::inNetwork($ip, $w)) {
                return true;
            }
        }
        return false;
    }

    /** This server's own addresses and loopback ("local addresses"). */
    public function isLocal(string $ip): bool
    {
        if (self::inNetwork($ip, '127.0.0.0/8') || $ip === '::1') {
            return true;
        }
        return $this->db->scalar('SELECT 1 FROM ip_addresses WHERE address = ?', [$ip]) !== null;
    }

    /** @return array<string, string> country code => whitelist|blacklist */
    public function countries(): array
    {
        $out = [];
        foreach ($this->db->fetchAll('SELECT code, status FROM hulk_countries') as $r) {
            $out[(string) $r['code']] = (string) $r['status'];
        }
        return $out;
    }

    /**
     * @param array<string, string> $marks code => whitelist|blacklist|none
     */
    public function setCountries(array $marks): int
    {
        $n = 0;
        $this->db->transaction(function () use ($marks, &$n): void {
            foreach ($marks as $code => $status) {
                if (!isset(Countries::ALL[$code])) {
                    continue;
                }
                if ($status === 'whitelist' || $status === 'blacklist') {
                    $this->db->run('INSERT INTO hulk_countries (code, status) VALUES (?, ?) ON CONFLICT (code) DO UPDATE SET status = excluded.status', [$code, $status]);
                } else {
                    $this->db->run('DELETE FROM hulk_countries WHERE code = ?', [$code]);
                }
                $n++;
            }
        });
        $this->audit->record('hulk.countries', 'server', 'hulk', AuditLogger::SUCCESS, ['marked' => $marks]);
        return $n;
    }

    private function countryStatus(string $ip): string
    {
        $cc = $this->geo->country($ip);
        return $cc === '' ? '' : (string) ($this->db->scalar('SELECT status FROM hulk_countries WHERE code = ?', [$cc]) ?? '');
    }

    // ---- Enforcement on WhaCP sign-ins ------------------------------------

    /** Why a WhaCP sign-in must be refused now, or null. */
    public function loginRefusal(string $username, string $ip): ?string
    {
        if (!$this->enabled() || $this->whitelisted($ip)) {
            return null;
        }
        $country = $this->countryStatus($ip);
        if ($country === 'whitelist') {
            return null;
        }
        if ($country === 'blacklist') {
            return 'Sign-ins from your country are not allowed on this server.';
        }
        $now = $this->now();
        if ($this->db->scalar("SELECT 1 FROM hulk_blocks WHERE ip = ? AND kind IN ('ip', 'oneday') AND expires_at > ?", [$ip, $now]) !== null) {
            return 'Your IP address is blocked by brute force protection. Try again later.';
        }
        if ($username !== '' && $this->db->scalar("SELECT 1 FROM hulk_blocks WHERE user = ? AND kind = 'user' AND expires_at > ?", [$username, $now]) !== null) {
            return 'This account is locked by brute force protection because of too many failed logins. Try again later.';
        }
        return null;
    }

    /** A failed WhaCP sign-in: counted, and acted on at once. */
    public function recordPanelFailure(string $username, string $ip): void
    {
        if (!$this->enabled()) {
            return;
        }
        $ev = ['id' => 'p:' . bin2hex(random_bytes(8)), 'time' => $this->now(), 'service' => 'whacp', 'authservice' => 'whacpd', 'user' => mb_substr($username, 0, 190), 'ip' => $ip, 'ok' => false];
        $this->store([$ev]);
        if ($this->evaluate([$ev]) > 0) {
            $this->syncQuietly();
        }
    }

    // ---- Scheduler --------------------------------------------------------

    /** @return list<string> log lines */
    public function run(): array
    {
        $lines = [];
        if (!$this->enabled()) {
            if ($this->settings->string('hulk_synced') !== 'off') {
                $this->agent->call('hulk.sync', ['enabled' => false], 60);
                $this->settings->update(['hulk_synced' => 'off'], null);
            }
            return [];
        }
        try {
            if ($this->countries() !== [] || $this->geo->loaded()) {
                if (($n = $this->geo->refresh()) !== null) {
                    $lines[] = "brute force protection: country database updated ({$n} ranges)";
                }
            }
        } catch (\Throwable $e) {
            $lines[] = 'brute force protection: the country database was not updated: ' . $e->getMessage();
        }
        $now = $this->now();
        $since = max((int) $this->db->scalar('SELECT MAX(time) FROM hulk_events WHERE server_id = ? AND id NOT LIKE ?', [$this->server->id(), 'p:%']) - 60, $now - $this->settings->int('hulk_retention') * 60);
        $res = (array) $this->agent->call('hulk.failures', ['since' => $since], 120);
        $events = array_map(static fn ($e): array => (array) $e, (array) ($res['events'] ?? []));
        $new = $this->store($events);
        $blocks = $this->evaluate($new);
        if ($blocks > 0) {
            $lines[] = "brute force protection: {$blocks} new block(s)";
        }
        $this->db->run('DELETE FROM hulk_events WHERE time < ?', [$now - $this->settings->int('hulk_retention') * 60]);
        $this->db->run('DELETE FROM hulk_blocks WHERE expires_at < ?', [$now - 86400]);
        $this->sync();
        $this->settings->update(['hulk_synced' => 'on'], null);
        return $lines;
    }

    /**
     * Keep events not seen before (with their country).
     *
     * @param list<array<string, mixed>> $events
     * @return list<array<string, mixed>> the new ones
     */
    private function store(array $events): array
    {
        $new = [];
        foreach ($events as $e) {
            $ip = (string) ($e['ip'] ?? '');
            $id = (string) ($e['id'] ?? '');
            if ($id === '' || @inet_pton($ip) === false) {
                continue;
            }
            $e['country'] = $this->geo->country($ip);
            $n = $this->db->run('INSERT OR IGNORE INTO hulk_events (id, server_id, time, service, authservice, user, ip, country, ok) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)', [
                $id, $this->server->id(), (int) $e['time'], mb_substr((string) $e['service'], 0, 40), mb_substr((string) $e['authservice'], 0, 60),
                mb_substr((string) ($e['user'] ?? ''), 0, 190), $ip, $e['country'], empty($e['ok']) ? 0 : 1,
            ])->rowCount();
            if ($n > 0) {
                $new[] = $e;
            }
        }
        return $new;
    }

    /**
     * Apply the rules to new events. Returns the number of new blocks.
     *
     * @param list<array<string, mixed>> $events
     */
    private function evaluate(array $events): int
    {
        $s = $this->settings;
        $now = $this->now();
        $made = 0;
        foreach ($events as $e) {
            $ip = (string) $e['ip'];
            $user = (string) ($e['user'] ?? '');
            if (!empty($e['ok'])) {
                if ($user === 'root') {
                    $this->rootLogin($ip, (int) $e['time'], (string) $e['authservice']);
                }
                continue;
            }
            if ($this->whitelisted($ip) || $this->countryStatus($ip) === 'whitelist') {
                continue;
            }
            $local = $this->isLocal($ip);
            // IP address-based protection (never this server's own addresses).
            if ($s->bool('hulk_ip_enabled') && !$local) {
                $period = $s->int('hulk_ip_period') * 60;
                $count = (int) $this->db->scalar('SELECT COUNT(*) FROM hulk_events WHERE ip = ? AND ok = 0 AND time > ?', [$ip, $now - $period]);
                $day = (int) $this->db->scalar('SELECT COUNT(*) FROM hulk_events WHERE ip = ? AND ok = 0 AND time > ?', [$ip, $now - 86400]);
                if ($day >= $s->int('hulk_oneday_max') && !$this->active('oneday', 'ip', $ip)) {
                    $made += $this->block('oneday', $ip, null, $e, $day, $s->int('hulk_oneday_max'), $now + 86400, $s->bool('hulk_oneday_firewall'), 'Too many failed logins in one day', $s->string('hulk_oneday_command'), true);
                } elseif ($count >= $s->int('hulk_ip_max') && !$this->active('ip', 'ip', $ip) && !$this->active('oneday', 'ip', $ip)) {
                    $made += $this->block('ip', $ip, null, $e, $count, $s->int('hulk_ip_max'), $now + $period, $s->bool('hulk_ip_firewall'), 'Too many failed logins from the address', $s->string('hulk_ip_command'), false);
                }
            }
            // Username-based protection.
            if ($s->bool('hulk_user_enabled') && $user !== '' && ($s->string('hulk_user_scope') !== 'local' || $local)) {
                if (!$s->bool('hulk_user_lock_root') && $this->privileged($user)) {
                    continue;
                }
                $period = $s->int('hulk_user_period') * 60;
                $sql = 'SELECT COUNT(*) FROM hulk_events WHERE user = ? AND ok = 0 AND time > ?';
                $count = (int) $this->db->scalar($sql, [$user, $now - $period]);
                if ($count >= $s->int('hulk_user_max') && !$this->active('user', 'user', $user)) {
                    $made += $this->block('user', null, $user, $e, $count, $s->int('hulk_user_max'), $now + $period, false, 'Too many failed logins for the account', '', false);
                }
            }
        }
        return $made;
    }

    /** "root", and WhaCP administrator accounts (they follow the root option). */
    private function privileged(string $user): bool
    {
        if ($user === 'root') {
            return true;
        }
        return $this->db->scalar("SELECT 1 FROM users u JOIN roles r ON r.id = u.role_id WHERE (u.username = ? OR u.email = ?) AND r.name = 'admin'", [$user, $user]) !== null;
    }

    private function active(string $kind, string $col, string $value): bool
    {
        return $this->db->scalar("SELECT 1 FROM hulk_blocks WHERE kind = ? AND {$col} = ? AND expires_at > ?", [$kind, $value, $this->now()]) !== null;
    }

    /** @param array<string, mixed> $e the last event */
    private function block(string $kind, ?string $ip, ?string $user, array $e, int $failures, int $max, int $expires, bool $firewall, string $reason, string $command, bool $excessive): int
    {
        $this->db->run('INSERT INTO hulk_blocks (kind, ip, user, reason, service, authservice, failures, firewall, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)', [
            $kind, $ip, $user, $reason, (string) $e['service'], (string) $e['authservice'], $failures, $firewall ? 1 : 0, $this->now(), $expires,
        ]);
        $what = $user !== null ? 'account ' . $user : $ip;
        $this->audit->record('hulk.block', 'server', 'hulk', AuditLogger::SUCCESS, ['kind' => $kind, 'ip' => $ip, 'user' => $user, 'failures' => $failures, 'until' => gmdate('c', $expires), 'firewall' => $firewall]);
        if ($this->settings->bool('hulk_notify_brute')) {
            $this->notifications->notify('hulk:' . $kind . ':' . $what . ':' . $expires, 'security', 'warning', 'Brute force ' . ($user !== null ? 'user' : 'attack') . ' detected: ' . $what, [
                'body' => sprintf('%s: %d failed logins (%s). Blocked until %s UTC%s.', $reason, $failures, (string) $e['authservice'], gmdate('Y-m-d H:i', $expires), $firewall ? ' at the firewall level' : ''),
                'link' => '/security/hulk#report',
            ]);
        }
        if ($command !== '' && $ip !== null) {
            $this->runCommand($command, [
                'exptime' => (string) $expires, 'max_allowed_failures' => (string) $max, 'current_failures' => (string) $failures,
                'excessive_failures' => $excessive ? '1' : '0', 'reason' => $reason, 'remote_ip' => $ip,
                'authservice' => (string) $e['authservice'], 'user' => (string) ($e['user'] ?? ''),
                'logintime' => (string) (int) $e['time'], 'ip_version' => str_contains($ip, ':') ? '6' : '4',
            ]);
        }
        return 1;
    }

    /** @param array<string, string> $vars */
    public static function expand(string $command, array $vars): string
    {
        return (string) preg_replace_callback('/%([a-z_]+)%/', static fn (array $m): string => array_key_exists($m[1], $vars) ? escapeshellarg($vars[$m[1]]) : $m[0], $command);
    }

    /**
     * Run an administrator's command (as the panel's account, 15 seconds at
     * most). The outcome goes to the audit log.
     *
     * @param array<string, string> $vars
     */
    private function runCommand(string $command, array $vars): void
    {
        $cmd = self::expand($command, $vars);
        $proc = @proc_open(['/bin/sh', '-c', $cmd], [0 => ['file', '/dev/null', 'r'], 1 => ['file', '/dev/null', 'w'], 2 => ['file', '/dev/null', 'w']], $pipes, null, ['PATH' => '/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin']);
        if (!is_resource($proc)) {
            $this->audit->record('hulk.command', 'server', 'hulk', AuditLogger::FAILURE, ['error' => 'could not start']);
            return;
        }
        $deadline = microtime(true) + self::COMMAND_TIMEOUT;
        $status = proc_get_status($proc);
        while ($status['running'] && microtime(true) < $deadline) {
            usleep(50_000);
            $status = proc_get_status($proc);
        }
        if ($status['running']) {
            proc_terminate($proc, 9);
            proc_close($proc);
            $this->audit->record('hulk.command', 'server', 'hulk', AuditLogger::FAILURE, ['error' => 'timed out after ' . self::COMMAND_TIMEOUT . ' seconds', 'ip' => $vars['remote_ip']]);
            return;
        }
        proc_close($proc);
        $this->audit->record('hulk.command', 'server', 'hulk', $status['exitcode'] === 0 ? AuditLogger::SUCCESS : AuditLogger::FAILURE, ['exit' => $status['exitcode'], 'ip' => $vars['remote_ip']]);
    }

    /** Root login notifications. */
    private function rootLogin(string $ip, int $time, string $service): void
    {
        if ($this->whitelisted($ip)) {
            return;
        }
        $netblock = str_contains($ip, ':') ? '/64' : '/24';
        $known = false;
        foreach (array_merge($this->whitelist(), array_map('strval', array_column($this->db->fetchAll("SELECT DISTINCT ip FROM hulk_events WHERE user = 'root' AND ok = 1 AND ip <> ?", [$ip]), 'ip'))) as $ref) {
            $base = explode('/', $ref)[0];
            if (self::inNetwork($ip, $base . $netblock)) {
                $known = true;
                break;
            }
        }
        if ($known ? !$this->settings->bool('hulk_notify_root_netblock') : !$this->settings->bool('hulk_notify_root')) {
            return;
        }
        $this->notifications->notify('hulk:root:' . $ip . ':' . $time, 'security', 'warning', 'Root login from ' . $ip, [
            'body' => sprintf('root signed in with %s from %s at %s UTC. The address is not on the brute force protection whitelist%s.', $service, $ip, gmdate('Y-m-d H:i', $time), $known ? ' (it is in a known netblock)' : ''),
            'link' => '/security/hulk',
        ]);
    }

    // ---- Server enforcement ------------------------------------------------

    /** Send the blocks, blacklisted countries and whitelist to the agent. */
    public function sync(): void
    {
        if (!$this->enabled()) {
            $this->agent->call('hulk.sync', ['enabled' => false], 60);
            return;
        }
        $blocks = [];
        foreach ($this->db->fetchAll("SELECT ip, MAX(expires_at) AS until, MAX(firewall) AS firewall FROM hulk_blocks WHERE ip IS NOT NULL AND kind IN ('ip', 'oneday') AND expires_at > ? GROUP BY ip", [$this->now()]) as $b) {
            $blocks[] = ['ip' => (string) $b['ip'], 'until' => (int) $b['until'], 'all' => (int) $b['firewall'] === 1];
        }
        $black = array_keys(array_filter($this->countries(), static fn (string $s): bool => $s === 'blacklist'));
        $this->agent->call('hulk.sync', ['enabled' => true, 'blocks' => $blocks, 'countries' => $this->geo->ranges($black), 'whitelist' => $this->whitelist()], 120);
    }

    private function syncQuietly(): void
    {
        try {
            $this->sync();
        } catch (AgentException $e) {
            $this->log->warning('brute force protection: blocks not applied yet', ['error' => $e->getMessage()]);
        }
    }

    // ---- Report -------------------------------------------------------------

    /** @return list<array<string, mixed>> failed logins in the retention period */
    public function report(int $limit = 1000): array
    {
        $retention = $this->settings->int('hulk_retention') * 60;
        $now = $this->now();
        $rows = [];
        foreach ($this->db->fetchAll('SELECT user, ip, country, service, authservice, time FROM hulk_events WHERE ok = 0 AND time > ? ORDER BY time DESC LIMIT ' . max(1, min(5000, $limit)), [$now - $retention]) as $r) {
            $exp = (int) $r['time'] + $retention;
            $r['expires'] = $exp;
            $r['minutes_left'] = max(0, (int) ceil(($exp - $now) / 60));
            $r['country_name'] = $r['country'] === '' ? '' : Countries::name((string) $r['country']);
            $rows[] = $r;
        }
        return $rows;
    }

    /** @return list<array<string, mixed>> blocks in force */
    public function blocks(): array
    {
        return $this->db->fetchAll('SELECT * FROM hulk_blocks WHERE expires_at > ? ORDER BY created_at DESC', [$this->now()]);
    }

    public function removeBlock(int $id): void
    {
        $this->db->run('DELETE FROM hulk_blocks WHERE id = ?', [$id]);
        $this->audit->record('hulk.unblock', 'server', 'hulk', AuditLogger::SUCCESS, ['id' => $id]);
        $this->syncQuietly();
    }

    /** "Remove Blocks and clear report". */
    public function clear(): void
    {
        $this->db->run('DELETE FROM hulk_blocks');
        $this->db->run('DELETE FROM hulk_events');
        $this->audit->record('hulk.clear', 'server', 'hulk', AuditLogger::SUCCESS, []);
        $this->syncQuietly();
    }
}
