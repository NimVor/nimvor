<?php

declare(strict_types=1);

namespace NimVorSite\Services;

use NimVorSite\Repositories\Telemetry;

/**
 * Receives the opt-in reports of NimVor installations (POST /api/v1/telemetry/{register,heartbeat,inventory,
 * features,health,unregister}; docs/telemetry/README.md has the contract).
 *
 * What is accepted is a closed list: every field has a type and a range, names come from allow-lists, and
 * anything that could identify a server or a person (free text, host names, addresses, domains) has no field to
 * go in. Memory and disk sizes are rounded to buckets, percentages to steps of ten.
 * Each report carries an event id, so a retry after a lost answer is recognised and counted once.
 */
final class TelemetryService
{
    public const SCHEMAS = [1];
    public const MAX_BODY = 16384;
    public const COMPONENTS = ['nginx', 'apache', 'php', 'mariadb', 'mysql', 'postgresql', 'mongodb', 'redis', 'memcached', 'bind', 'nsd', 'knot', 'powerdns', 'postfix', 'dovecot', 'exim', 'rspamd', 'clamav', 'certbot', 'nodejs', 'python', 'fail2ban', 'firewalld', 'ufw', 'nftables', 'modsecurity', 'php-fpm', 'restic', 'rsync', 'git'];
    public const FEATURES = ['websites', 'domains', 'dns_zones', 'mail_domains', 'mailboxes', 'databases', 'postgres_databases', 'mongo_databases', 'ssl_certificates', 'backups', 'backup_schedules', 'cron_jobs', 'ftp_accounts', 'wordpress_sites', 'node_apps', 'python_apps', 'panel_users', 'remote_servers', 'migrations', 'firewall_rules', 'plugins', 'php_versions', 'alert_rules', 'dashboard_customised'];
    public const OS_IDS = ['ubuntu', 'debian', 'almalinux', 'rocky', 'rhel', 'centos', 'fedora', 'other'];
    public const VIRT = ['none', 'kvm', 'qemu', 'vmware', 'xen', 'hyperv', 'virtualbox', 'lxc', 'docker', 'openvz', 'other', 'unknown'];
    private const VERSION = '/^[0-9]{1,3}(\.[0-9]{1,3}){1,3}(-[0-9A-Za-z.]{1,12})?$/';
    private const UUID = '/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/';

    /** requests per installation per hour, by endpoint */
    private const LIMITS = ['register' => 5, 'heartbeat' => 12, 'inventory' => 6, 'features' => 6, 'health' => 12, 'unregister' => 5];

    public function __construct(private readonly Telemetry $repo, private readonly RateLimiter $limiter)
    {
    }

    /**
     * @param array<string, mixed> $d the decoded JSON body
     * @return array{0: int, 1: array<string, mixed>, 2: array<string, string>} status, JSON answer, extra headers
     */
    public function handle(string $endpoint, array $d, string $bearer, string $ip): array
    {
        $schema = is_int($d['schema'] ?? null) ? $d['schema'] : null;
        $res = $this->dispatch($endpoint, $d, $bearer, $ip, $schema);
        try {
            $this->repo->logApi($endpoint, $res[0], (string) ($res[1]['error'] ?? ''), $schema);
        } catch (\Throwable) {
            // Keeping the log never decides the answer.
        }
        return $res;
    }

    /** @param array<string, mixed> $d @return array{0: int, 1: array<string, mixed>, 2: array<string, string>} */
    private function dispatch(string $endpoint, array $d, string $bearer, string $ip, ?int $schema): array
    {
        if (!isset(self::LIMITS[$endpoint])) {
            return $this->fail(404, 'unknown_endpoint');
        }
        if ($this->limiter->hit('tel:ip:' . $ip, 600, 3600)) {
            return $this->fail(429, 'rate_limited', ['Retry-After' => '3600']);
        }
        if ($schema === null || !in_array($schema, self::SCHEMAS, true)) {
            return $this->fail(400, 'unsupported_schema', [], ['supported' => self::SCHEMAS]);
        }
        $uuid = is_string($d['installation_uuid'] ?? null) ? strtolower($d['installation_uuid']) : '';
        if (preg_match(self::UUID, $uuid) !== 1) {
            return $this->invalid('installation_uuid');
        }
        if ($this->limiter->hit("tel:{$endpoint}:{$uuid}", self::LIMITS[$endpoint], 3600)) {
            return $this->fail(429, 'rate_limited', ['Retry-After' => '3600']);
        }
        if ($this->repo->wasDeleted($uuid)) {
            return $this->fail(410, 'deleted');
        }
        $event = is_string($d['event_id'] ?? null) ? strtolower($d['event_id']) : '';
        if (preg_match(self::UUID, $event) !== 1) {
            return $this->invalid('event_id');
        }
        if ($endpoint === 'register') {
            return $this->register($uuid, $d, $schema);
        }

        $inst = $this->repo->byUuid($uuid);
        if ($inst === null || $bearer === '' || !hash_equals((string) $inst['token_hash'], hash('sha256', $bearer))) {
            return $this->fail(401, 'unauthorized'); // the same answer for "unknown id" and "wrong token"
        }
        $id = (int) $inst['id'];
        if ($endpoint === 'unregister') {
            return $this->unregister($id, $uuid, $d);
        }
        if ($inst['status'] !== 'active') {
            return $this->fail(410, 'unregistered');
        }
        $result = match ($endpoint) {
            'heartbeat' => $this->heartbeat($id, $event, $d),
            'inventory' => $this->inventory($id, $event, $d),
            'features' => $this->features($id, $event, $d),
            default => $this->health($id, $event, $d),
        };
        if ($result[0] < 300) {
            $this->repo->touch($id, $endpoint === 'heartbeat' ? (string) $d['nimvor_version'] : null, $endpoint === 'heartbeat' && isset($d['agent_version']) ? (string) $d['agent_version'] : null);
        }
        return $result;
    }

    /** @param array<string, mixed> $d @return array{0: int, 1: array<string, mixed>, 2: array<string, string>} */
    private function register(string $uuid, array $d, int $schema): array
    {
        $version = $this->version($d['nimvor_version'] ?? null);
        $agent = isset($d['agent_version']) ? $this->version($d['agent_version']) : '';
        if ($version === null || $agent === null) {
            return $this->invalid($version === null ? 'nimvor_version' : 'agent_version');
        }
        $token = bin2hex(random_bytes(24));
        $existing = $this->repo->byUuid($uuid);
        if ($existing === null) {
            $this->repo->create($uuid, hash('sha256', $token), $schema, $version, $agent);
        } elseif ((int) $existing['token_confirmed'] === 0 && $existing['status'] === 'active') {
            // The first answer was lost on the way: the installation never used its token, so it may have a new one.
            $this->repo->replaceToken((int) $existing['id'], hash('sha256', $token), $version, $agent);
        } else {
            return $this->fail(409, 'already_registered');
        }
        return [201, ['status' => 'registered', 'token' => $token, 'heartbeat_every_hours' => 24, 'inventory_every_days' => 7], ['Cache-Control' => 'no-store']];
    }

    /** @param array<string, mixed> $d @return array{0: int, 1: array<string, mixed>, 2: array<string, string>} */
    private function unregister(int $id, string $uuid, array $d): array
    {
        $erase = ($d['delete'] ?? false) === true;
        if ($erase) {
            $this->repo->erase($uuid);
            return [200, ['status' => 'deleted'], []];
        }
        $this->repo->unregister($id);
        return [200, ['status' => 'unregistered'], []];
    }

    /** @param array<string, mixed> $d @return array{0: int, 1: array<string, mixed>, 2: array<string, string>} */
    private function heartbeat(int $id, string $event, array $d): array
    {
        $version = $this->version($d['nimvor_version'] ?? null);
        $agent = isset($d['agent_version']) ? $this->version($d['agent_version']) : '';
        $uptime = $d['uptime_days'] ?? null;
        if ($version === null) {
            return $this->invalid('nimvor_version');
        }
        if ($agent === null) {
            return $this->invalid('agent_version');
        }
        if ($uptime !== null && (!is_int($uptime) || $uptime < 0 || $uptime > 36500)) {
            return $this->invalid('uptime_days');
        }
        $events = $d['events'] ?? [];
        if (!is_array($events) || count($events) > 20) {
            return $this->invalid('events');
        }
        $rows = [];
        foreach ($events as $e) {
            $kind = is_array($e) ? ($e['kind'] ?? null) : null;
            $eid = is_array($e) && is_string($e['event_id'] ?? null) ? strtolower($e['event_id']) : '';
            $from = is_array($e) && isset($e['from_version']) ? $this->version($e['from_version']) : '';
            $to = is_array($e) ? $this->version($e['to_version'] ?? null) : null;
            $when = is_array($e) && isset($e['occurred_at']) ? $this->time($e['occurred_at']) : null;
            if (!in_array($kind, ['install', 'upgrade', 'downgrade', 'rollback'], true) || preg_match(self::UUID, $eid) !== 1 || $from === null || $to === null || !is_bool($e['success'] ?? null) || (isset($e['occurred_at']) && $when === null)) {
                return $this->invalid('events');
            }
            $rows[] = [$eid, (string) $kind, $from, $to, $e['success'], $when];
        }
        if (!$this->repo->addHeartbeat($id, $event, $version, $agent, $uptime)) {
            return [200, ['status' => 'duplicate'], []];
        }
        foreach ($rows as [$eid, $kind, $from, $to, $ok, $when]) {
            $this->repo->addRelease($id, $eid, $kind, $from, $to, $ok, $when);
        }
        return [200, ['status' => 'ok'], []];
    }

    /** @param array<string, mixed> $d @return array{0: int, 1: array<string, mixed>, 2: array<string, string>} */
    private function inventory(int $id, string $event, array $d): array
    {
        $o = $d['os'] ?? null;
        $sw = $d['software'] ?? [];
        if (!is_array($o)) {
            return $this->invalid('os');
        }
        $osId = $this->word($o['id'] ?? null, self::OS_IDS, 'other');
        $osVersion = is_string($o['version'] ?? null) && preg_match('/^[0-9]{1,2}(\.[0-9]{1,2})?$/', $o['version']) === 1 ? $o['version'] : null;
        $arch = $this->word($o['arch'] ?? null, ['amd64', 'arm64', 'other'], 'other');
        $kernel = is_string($o['kernel_major'] ?? null) && preg_match('/^[0-9]{1,2}\.[0-9]{1,3}$/', $o['kernel_major']) === 1 ? $o['kernel_major'] : '';
        $cores = $o['cpu_cores'] ?? null;
        $mem = $o['memory_mb'] ?? null;
        $disk = $o['disk_gb'] ?? null;
        if ($osVersion === null) {
            return $this->invalid('os.version');
        }
        foreach (['cpu_cores' => [$cores, 1024], 'memory_mb' => [$mem, 16777216], 'disk_gb' => [$disk, 1048576]] as $name => [$v, $max]) {
            if ($v !== null && (!is_int($v) || $v < 0 || $v > $max)) {
                return $this->invalid('os.' . $name);
            }
        }
        if (!is_array($sw) || count($sw) > 60) {
            return $this->invalid('software');
        }
        $list = [];
        foreach ($sw as $s) {
            $component = is_array($s) && is_string($s['component'] ?? null) ? $s['component'] : '';
            $ver = is_array($s) && isset($s['version']) ? $this->componentVersion($s['version']) : '';
            if (!in_array($component, self::COMPONENTS, true) || $ver === null || (isset($s['running']) && !is_bool($s['running']))) {
                return $this->invalid('software');
            }
            $list[$component] = ['component' => $component, 'version' => $ver, 'running' => isset($s['running']) ? $s['running'] : null];
        }
        $family = in_array($osId, ['ubuntu', 'debian'], true) ? 'debian' : (in_array($osId, ['almalinux', 'rocky', 'rhel', 'centos', 'fedora'], true) ? 'rhel' : 'other');
        $fresh = $this->repo->replaceOs($id, $event, [
            'id' => $osId, 'version' => $osVersion, 'family' => $family, 'kernel_major' => $kernel, 'arch' => $arch,
            'virtualization' => $this->word($o['virtualization'] ?? null, self::VIRT, 'unknown'), 'init_system' => $this->word($o['init_system'] ?? null, ['systemd', 'openrc', 'sysv', 'other'], 'unknown'),
            'cpu_cores' => $cores, 'memory_mb_bucket' => $mem === null ? null : self::bucket($mem, [512, 1024, 2048, 4096, 8192, 16384, 32768, 65536, 131072]), 'disk_gb_bucket' => $disk === null ? null : self::bucket($disk, [20, 40, 80, 160, 320, 640, 1280, 2560]),
        ]);
        if (!$fresh) {
            return [200, ['status' => 'duplicate'], []];
        }
        $this->repo->replaceSoftware($id, array_values($list));
        return [200, ['status' => 'ok'], []];
    }

    /** @param array<string, mixed> $d @return array{0: int, 1: array<string, mixed>, 2: array<string, string>} */
    private function features(int $id, string $event, array $d): array
    {
        $f = $d['features'] ?? null;
        if (!is_array($f) || $f === [] || count($f) > 60) {
            return $this->invalid('features');
        }
        $clean = [];
        $ignored = 0;
        foreach ($f as $name => $n) {
            if (!is_int($n) || $n < 0 || $n > 10000000) {
                return $this->invalid('features.' . (is_string($name) ? substr(preg_replace('/[^a-z_]/', '', $name) ?? '', 0, 30) : 'x'));
            }
            in_array($name, self::FEATURES, true) ? $clean[(string) $name] = $n : $ignored++;
        }
        if ($clean === []) {
            return $this->invalid('features');
        }
        return $this->repo->addFeatures($id, $event, $clean) ? [200, ['status' => 'ok', 'ignored' => $ignored], []] : [200, ['status' => 'duplicate'], []];
    }

    /** @param array<string, mixed> $d @return array{0: int, 1: array<string, mixed>, 2: array<string, string>} */
    private function health(int $id, string $event, array $d): array
    {
        $h = [];
        foreach (['services_total' => 5000, 'services_failed' => 5000, 'failed_jobs' => 100000] as $k => $max) {
            $v = $d[$k] ?? null;
            if ($v !== null && (!is_int($v) || $v < 0 || $v > $max)) {
                return $this->invalid($k);
            }
            $h[$k] = $v;
        }
        foreach (['disk_used_percent' => 'disk_used_bucket', 'memory_used_percent' => 'memory_used_bucket'] as $in => $col) {
            $v = $d[$in] ?? null;
            if ($v !== null && (!is_int($v) || $v < 0 || $v > 100)) {
                return $this->invalid($in);
            }
            $h[$col] = $v === null ? null : intdiv($v, 10) * 10;
        }
        foreach (['updates_pending', 'last_backup_ok', 'ssl_expiring'] as $k) {
            $v = $d[$k] ?? null;
            if ($v !== null && !is_bool($v)) {
                return $this->invalid($k);
            }
            $h[$k] = $v === null ? null : (int) $v;
        }
        if (array_filter($h, static fn (mixed $v): bool => $v !== null) === []) {
            return $this->invalid('health');
        }
        return $this->repo->addHealth($id, $event, $h) ? [200, ['status' => 'ok'], []] : [200, ['status' => 'duplicate'], []];
    }

    // ------------------------------------------------------------------ small helpers

    private function version(mixed $v): ?string
    {
        return is_string($v) && preg_match(self::VERSION, $v) === 1 ? $v : null;
    }

    /** A software version as the report gives it: major.minor, or empty. */
    private function componentVersion(mixed $v): ?string
    {
        return is_string($v) && ($v === '' || preg_match('/^[0-9]{1,3}(\.[0-9]{1,3}){0,2}$/', $v) === 1) ? $v : null;
    }

    private function time(mixed $v): ?string
    {
        if (!is_string($v) || ($t = strtotime($v)) === false || $t > time() + 300 || $t < time() - 86400 * 400) {
            return null;
        }
        return gmdate('Y-m-d H:i:s', $t);
    }

    /** @param list<string> $allowed */
    private function word(mixed $v, array $allowed, string $else): string
    {
        return is_string($v) && in_array(strtolower($v), $allowed, true) ? strtolower($v) : $else;
    }

    /** The smallest step that holds the value (the last one for anything bigger). @param list<int> $steps */
    public static function bucket(int $v, array $steps): int
    {
        foreach ($steps as $s) {
            if ($v <= $s) {
                return $s;
            }
        }
        return end($steps);
    }

    /** @return array{0: int, 1: array<string, mixed>, 2: array<string, string>} */
    private function invalid(string $field): array
    {
        return $this->fail(422, 'invalid_report', [], ['field' => $field]);
    }

    /**
     * @param array<string, string> $headers
     * @param array<string, mixed> $more
     * @return array{0: int, 1: array<string, mixed>, 2: array<string, string>}
     */
    private function fail(int $status, string $code, array $headers = [], array $more = []): array
    {
        return [$status, ['error' => $code] + $more, $headers];
    }
}
