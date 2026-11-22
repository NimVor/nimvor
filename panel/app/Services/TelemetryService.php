<?php

declare(strict_types=1);

namespace NimVor\Services;

use Closure;
use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;
use NimVor\Security\Crypto;

/**
 * Opt-in telemetry (docs/telemetry/README.md). Off until an administrator turns it on; nothing is sent, queued or
 * generated before that (not even the installation id).
 *
 * Reports go to the central interface (https://nimvor.com/api/v1/telemetry): registration, a heartbeat and a health
 * report every day, an inventory and feature counts every week. Each report is queued first and sent from the
 * scheduler, never while a page loads; a failure is retried with exponential back-off and jitter and a report is
 * dropped after seven days. What is sent is a fixed set of facts (see preview()): never an address, host name,
 * domain, user, e-mail, path or file content. The administrator can turn it off, unregister, or have everything
 * deleted at the central service.
 */
final class TelemetryService
{
    public const SCHEMA = 1;
    public const DEFAULT_URL = 'https://nimvor.com/api/v1/telemetry';
    public const DAY = 86400;
    public const DROP_AFTER = 7 * 86400;
    private const VERSION = '/^[0-9]{1,3}(\.[0-9]{1,3}){1,3}(-[0-9A-Za-z.]{1,12})?$/';
    /** how often each report is due, in seconds */
    private const EVERY = ['heartbeat' => 86400, 'health' => 86400, 'inventory' => 7 * 86400, 'features' => 7 * 86400];

    /** @var Closure(string, string, array<string, string>, string): array{0: int, 1: ?string} */
    private Closure $send;

    /**
     * @param (Closure(string, string, array<string, string>, string): array{0: int, 1: ?string})|null $send method, url, headers, body → [HTTP status (0 when unreachable), body]
     */
    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly Crypto $crypto,
        private readonly AuditLogger $audit,
        private readonly string $version,
        private readonly string $url = self::DEFAULT_URL,
        private readonly string $optInFile = '/etc/nimvor/telemetry-optin',
        private readonly string $eventsFile = '/etc/nimvor/release-events.jsonl',
        ?Closure $send = null,
    ) {
        $this->send = $send ?? static function (string $method, string $url, array $headers, string $body): array {
            $h = [];
            foreach ($headers as $k => $v) {
                $h[] = $k . ': ' . $v;
            }
            $ch = curl_init($url);
            curl_setopt_array($ch, [CURLOPT_RETURNTRANSFER => true, CURLOPT_TIMEOUT => 15, CURLOPT_CONNECTTIMEOUT => 6, CURLOPT_FOLLOWLOCATION => false, CURLOPT_PROTOCOLS => CURLPROTO_HTTPS,
                CURLOPT_USERAGENT => 'NimVor telemetry', CURLOPT_CUSTOMREQUEST => $method, CURLOPT_POSTFIELDS => $body, CURLOPT_HTTPHEADER => $h]);
            $out = curl_exec($ch);
            $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
            curl_close($ch);
            return [$code, is_string($out) ? $out : null];
        };
    }

    // ------------------------------------------------------------------ the choice

    public function enabled(): bool
    {
        return $this->get('enabled') === '1';
    }

    /** Whether the administrator (or the installer's question) has answered yet. */
    public function decided(): bool
    {
        return $this->get('decided') === '1';
    }

    public function enable(?int $userId = null): void
    {
        $this->set('decided', '1');
        $this->set('enabled', '1');
        if ($this->get('uuid') === null) {
            $this->set('uuid', self::uuid());
        }
        $this->audit->record('telemetry.enable', 'telemetry', '', AuditLogger::SUCCESS, [], $userId);
    }

    /** Stops the reports and forgets what was waiting. The id stays, so turning it on again continues the same installation. */
    public function disable(?int $userId = null): void
    {
        $this->set('decided', '1');
        $this->set('enabled', '0');
        $this->db->run("DELETE FROM telemetry_queue WHERE endpoint <> 'unregister'");
        $this->audit->record('telemetry.disable', 'telemetry', '', AuditLogger::SUCCESS, [], $userId);
    }

    /**
     * Tells the central service this installation stops reporting; with $delete it also erases what was stored
     * there. The local identity is forgotten once the service has answered; until then the request is retried.
     */
    public function unregister(bool $delete, ?int $userId = null): void
    {
        $this->set('decided', '1');
        $this->set('enabled', '0');
        $this->db->run('DELETE FROM telemetry_queue');
        if ($this->get('uuid') !== null && $this->get('registered') === '1') {
            $this->queue('unregister', ['delete' => $delete]);
            $this->flush();
        } else {
            $this->forgetIdentity();
        }
        $this->audit->record($delete ? 'telemetry.delete' : 'telemetry.unregister', 'telemetry', '', AuditLogger::SUCCESS, [], $userId);
    }

    /** @return array<string, mixed> what the page shows */
    public function status(): array
    {
        $pending = (int) $this->db->scalar('SELECT COUNT(*) FROM telemetry_queue');
        return [
            'enabled' => $this->enabled(), 'decided' => $this->decided(), 'uuid' => $this->get('uuid'), 'registered' => $this->get('registered') === '1',
            'last_success' => $this->time('last_success'), 'last_error' => (string) ($this->get('last_error') ?? ''), 'pending' => $pending,
            'pending_deletion' => (bool) $this->db->scalar("SELECT COUNT(*) FROM telemetry_queue WHERE endpoint = 'unregister'"),
            'last' => ['heartbeat' => $this->time('last_heartbeat'), 'health' => $this->time('last_health'), 'inventory' => $this->time('last_inventory'), 'features' => $this->time('last_features')],
            'url' => $this->url,
        ];
    }

    // ------------------------------------------------------------------ the scheduler's pass

    /** @return list<string> log lines */
    public function run(): array
    {
        $this->adoptInstallerChoice();
        $pendingUnregister = (int) $this->db->scalar("SELECT COUNT(*) FROM telemetry_queue WHERE endpoint = 'unregister'") > 0;
        if (!$this->enabled() && !$pendingUnregister) {
            return [];
        }
        if ($this->enabled()) {
            $this->enqueueDue();
        }
        return $this->flush();
    }

    /** The installer's question: a yes is recorded in a small file the panel may read. It counts once. */
    private function adoptInstallerChoice(): void
    {
        if ($this->decided() || !is_file($this->optInFile)) {
            return;
        }
        $this->enable(null);
        $this->set('last_error', '');
    }

    private function enqueueDue(): void
    {
        if ($this->get('uuid') === null) {
            $this->set('uuid', self::uuid());
        }
        if ($this->get('registered') !== '1') {
            if ($this->pending('register') === 0 && self::validVersion($this->version)) {
                $this->queue('register', ['nimvor_version' => $this->version, 'agent_version' => $this->agentVersion()]);
            }
            return; // nothing else is sent before the central service has answered the registration
        }
        foreach (self::EVERY as $endpoint => $every) {
            if ($this->pending($endpoint) > 0 || time() - (int) ($this->get('last_' . $endpoint) ?? 0) < $every) {
                continue;
            }
            $body = $this->body($endpoint);
            if ($body !== null) {
                $this->queue($endpoint, $body);
            } else {
                $this->set('last_' . $endpoint, (string) (time() - $every + 3600)); // could not be built now: try again in an hour
            }
        }
    }

    // ------------------------------------------------------------------ what each report contains

    /** @return array<string, mixed>|null null when nothing can be reported now */
    private function body(string $endpoint): ?array
    {
        return match ($endpoint) {
            'heartbeat' => $this->heartbeat(),
            'inventory' => $this->inventory(),
            'health' => $this->health(),
            'features' => $this->features(),
            default => null,
        };
    }

    /** @return array<string, mixed>|null */
    private function heartbeat(): ?array
    {
        if (!self::validVersion($this->version)) {
            return null; // a development build is not a release
        }
        $body = ['nimvor_version' => $this->version, 'agent_version' => $this->agentVersion()];
        try {
            $info = (array) $this->agent->call('system.info');
            $up = (float) ($info['system']['uptime_seconds'] ?? 0);
            if ($up > 0) {
                $body['uptime_days'] = (int) floor($up / 86400);
            }
        } catch (AgentException) {
        }
        $events = $this->releaseEvents();
        if ($events !== []) {
            $body['events'] = $events;
        }
        return $body;
    }

    /** @return array<string, mixed>|null */
    private function inventory(): ?array
    {
        $c = $this->collect();
        return $c === null ? null : ['os' => (array) ($c['os'] ?? []), 'software' => array_values((array) ($c['software'] ?? []))];
    }

    /** @return array<string, mixed>|null */
    private function health(): ?array
    {
        $c = $this->collect();
        $h = $c === null ? [] : (array) ($c['health'] ?? []);
        $latest = $this->db->scalar("SELECT status FROM backups WHERE status <> 'running' ORDER BY id DESC LIMIT 1");
        if ($latest !== null && $latest !== false) {
            $h['last_backup_ok'] = in_array((string) $latest, ['ok', 'completed', 'done', 'success'], true);
        }
        $failed = $this->db->scalar("SELECT COUNT(*) FROM agent_jobs WHERE status = 'failed' AND created_at >= ?", [gmdate('c', time() - 86400)]);
        $h['failed_jobs'] = (int) $failed;
        return $h === [] ? null : $h;
    }

    /** @return array<string, int> */
    private function counts(): array
    {
        $tables = ['websites' => 'websites', 'domains' => 'domains', 'dns_zones' => 'dns_zones', 'mail_domains' => 'mail_domains', 'mailboxes' => 'mail_mailboxes', 'databases' => 'databases',
            'postgres_databases' => 'pg_databases', 'backups' => 'backups', 'backup_schedules' => 'backup_schedules', 'cron_jobs' => 'cron_jobs', 'wordpress_sites' => 'wordpress_installs',
            'panel_users' => 'users', 'migrations' => 'site_migrations'];
        $out = [];
        foreach ($tables as $feature => $table) {
            try {
                $out[$feature] = (int) $this->db->scalar("SELECT COUNT(*) FROM {$table}");
            } catch (\Throwable) {
                // A table this version does not have is simply not reported.
            }
        }
        try {
            $out['remote_servers'] = max(0, (int) $this->db->scalar('SELECT COUNT(*) FROM servers') - 1);
        } catch (\Throwable) {
        }
        return $out;
    }

    /** @return array<string, mixed>|null */
    private function features(): ?array
    {
        $c = $this->counts();
        return $c === [] ? null : ['features' => $c];
    }

    /** @return array<string, mixed>|null */
    private function collect(): ?array
    {
        try {
            return (array) $this->agent->call('telemetry.collect', [], 60);
        } catch (AgentException) {
            return null;
        }
    }

    /**
     * What the next reports would contain, for the settings page. Built the same way as a real report but with
     * no ids; the agent is asked once.
     *
     * @return array<string, array<string, mixed>|null>
     */
    public function preview(): array
    {
        $c = $this->collect();
        $health = $c === null ? null : (array) ($c['health'] ?? []);
        if ($health !== null) {
            $latest = $this->db->scalar("SELECT status FROM backups WHERE status <> 'running' ORDER BY id DESC LIMIT 1");
            if ($latest !== null && $latest !== false) {
                $health['last_backup_ok'] = in_array((string) $latest, ['ok', 'completed', 'done', 'success'], true);
            }
        }
        return [
            'heartbeat' => $this->heartbeat() ?? ['note' => 'A development build sends no heartbeat.'],
            'inventory' => $c === null ? null : ['os' => (array) ($c['os'] ?? []), 'software' => array_values((array) ($c['software'] ?? []))],
            'features' => $this->features(),
            'health' => $health,
        ];
    }

    /** @return list<array<string, mixed>> install / upgrade events the installer noted, not yet sent */
    private function releaseEvents(): array
    {
        if (!is_file($this->eventsFile)) {
            return [];
        }
        $sent = (array) json_decode((string) ($this->get('events_sent') ?? '[]'), true);
        $out = [];
        foreach (array_slice((array) file($this->eventsFile, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES), -20) as $line) {
            $e = json_decode((string) $line, true);
            if (!is_array($e) || !is_string($e['event_id'] ?? null) || in_array($e['event_id'], $sent, true)) {
                continue;
            }
            $out[] = array_intersect_key($e, array_flip(['event_id', 'kind', 'from_version', 'to_version', 'success', 'occurred_at']));
        }
        return $out;
    }

    private function markEventsSent(array $body): void
    {
        $ids = array_map(static fn (array $e): string => (string) $e['event_id'], (array) ($body['events'] ?? []));
        if ($ids === []) {
            return;
        }
        $sent = (array) json_decode((string) ($this->get('events_sent') ?? '[]'), true);
        $this->set('events_sent', (string) json_encode(array_slice(array_values(array_unique(array_merge($sent, $ids))), -100)));
    }

    // ------------------------------------------------------------------ queue and delivery

    /** @param array<string, mixed> $body */
    private function queue(string $endpoint, array $body): void
    {
        $event = self::uuid();
        $payload = ['schema' => self::SCHEMA, 'installation_uuid' => (string) $this->get('uuid'), 'event_id' => $event] + $body;
        $this->db->run('INSERT INTO telemetry_queue (endpoint, event_id, payload, next_attempt_at, created_at) VALUES (?, ?, ?, ?, ?)', [$endpoint, $event, json_encode($payload, JSON_THROW_ON_ERROR), gmdate('c'), gmdate('c')]);
    }

    private function pending(string $endpoint): int
    {
        return (int) $this->db->scalar('SELECT COUNT(*) FROM telemetry_queue WHERE endpoint = ?', [$endpoint]);
    }

    /** @return list<string> */
    public function flush(): array
    {
        $log = [];
        foreach ($this->db->fetchAll('SELECT * FROM telemetry_queue WHERE next_attempt_at <= ? ORDER BY id', [gmdate('c')]) as $row) {
            $endpoint = (string) $row['endpoint'];
            if (strtotime((string) $row['created_at']) < time() - self::DROP_AFTER) {
                $this->db->run('DELETE FROM telemetry_queue WHERE id = ?', [$row['id']]);
                $log[] = "telemetry: a {$endpoint} report older than seven days was dropped";
                continue;
            }
            $headers = ['Content-Type' => 'application/json', 'Accept' => 'application/json'];
            if ($endpoint !== 'register') {
                $token = $this->token();
                if ($token === '') {
                    $this->db->run('DELETE FROM telemetry_queue WHERE id = ?', [$row['id']]);
                    continue;
                }
                $headers['Authorization'] = 'Bearer ' . $token;
            }
            try {
                [$status, $answer] = ($this->send)('POST', rtrim($this->url, '/') . '/' . $endpoint, $headers, (string) $row['payload']);
            } catch (\Throwable) {
                [$status, $answer] = [0, null];
            }
            $data = is_string($answer) ? (array) json_decode($answer, true) : [];
            if ($status >= 200 && $status < 300) {
                $this->delivered($endpoint, $row, $data);
                $log[] = "telemetry: {$endpoint} sent";
                continue;
            }
            $code = (string) ($data['error'] ?? '');
            if ($status === 409 || $status === 401) {
                // The id or its token is no longer usable here (an answer was lost, or the data was reset): start again with a new id.
                $this->db->run('DELETE FROM telemetry_queue WHERE id = ?', [$row['id']]);
                if ($endpoint !== 'unregister' && $this->enabled()) {
                    $this->forgetIdentity();
                    $this->set('uuid', self::uuid());
                    $this->set('last_error', 'The central service did not know this installation; it registers again.');
                } elseif ($endpoint === 'unregister') {
                    $this->forgetIdentity();
                }
                return $log;
            }
            if ($status === 410) {
                $this->db->run('DELETE FROM telemetry_queue WHERE id = ?', [$row['id']]);
                if ($endpoint === 'unregister') {
                    $this->forgetIdentity();
                } else {
                    $this->set('enabled', '0');
                    $this->set('last_error', 'The central service has no record of this installation, so reporting was turned off.');
                }
                continue;
            }
            if (in_array($status, [400, 413, 415, 422], true)) {
                $this->db->run('DELETE FROM telemetry_queue WHERE id = ?', [$row['id']]);
                $this->set('last_error', "The central service refused a {$endpoint} report ({$status} {$code}); it was dropped.");
                $log[] = "telemetry: {$endpoint} refused ({$status} {$code})";
                continue;
            }
            // Unreachable, rate limited or a server error: retry later with back-off, and stop for this pass.
            $attempts = (int) $row['attempts'] + 1;
            $wait = min(21600, 60 * (2 ** min($attempts, 10))) + random_int(0, 30);
            if ($status === 429) {
                $wait = max($wait, 3600);
            }
            $this->db->run('UPDATE telemetry_queue SET attempts = ?, next_attempt_at = ?, last_error = ? WHERE id = ?', [$attempts, gmdate('c', time() + $wait), $status === 0 ? 'unreachable' : (string) $status, $row['id']]);
            $this->set('last_error', $status === 0 ? 'The central service could not be reached; it is tried again later.' : "The central service answered {$status}; it is tried again later.");
            $log[] = "telemetry: {$endpoint} postponed ({$status})";
            return $log;
        }
        return $log;
    }

    /** @param array<string, mixed> $row @param array<string, mixed> $data */
    private function delivered(string $endpoint, array $row, array $data): void
    {
        $this->db->run('DELETE FROM telemetry_queue WHERE id = ?', [$row['id']]);
        $this->set('last_success', (string) time());
        $this->set('last_error', '');
        switch ($endpoint) {
            case 'register':
                if (is_string($data['token'] ?? null)) {
                    $this->set('token', $this->crypto->encrypt($data['token']));
                    $this->set('registered', '1');
                    $this->set('registered_at', (string) time());
                }
                break;
            case 'unregister':
                $this->forgetIdentity();
                break;
            default:
                $this->set('last_' . $endpoint, (string) time());
                if ($endpoint === 'heartbeat') {
                    $this->markEventsSent((array) json_decode((string) $row['payload'], true));
                }
        }
    }

    private function forgetIdentity(): void
    {
        foreach (['uuid', 'token', 'registered', 'registered_at', 'last_heartbeat', 'last_inventory', 'last_features', 'last_health', 'events_sent'] as $k) {
            $this->db->run('DELETE FROM telemetry_state WHERE key = ?', [$k]);
        }
    }

    // ------------------------------------------------------------------ small helpers

    private function token(): string
    {
        $t = $this->get('token');
        if ($t === null || $t === '') {
            return '';
        }
        try {
            return $this->crypto->decrypt($t);
        } catch (\Throwable) {
            return '';
        }
    }

    private function agentVersion(): string
    {
        try {
            $v = (string) (((array) $this->agent->call('system.info'))['agent_version'] ?? '');
            return self::validVersion($v) ? $v : '';
        } catch (AgentException) {
            return '';
        }
    }

    public static function validVersion(string $v): bool
    {
        return preg_match(self::VERSION, $v) === 1;
    }

    public static function uuid(): string
    {
        $b = random_bytes(16);
        $b[6] = chr((ord($b[6]) & 0x0f) | 0x40);
        $b[8] = chr((ord($b[8]) & 0x3f) | 0x80);
        return vsprintf('%s%s-%s-%s-%s-%s%s%s', str_split(bin2hex($b), 4));
    }

    private function get(string $key): ?string
    {
        $v = $this->db->scalar('SELECT value FROM telemetry_state WHERE key = ?', [$key]);
        return $v === null || $v === false ? null : (string) $v;
    }

    private function set(string $key, string $value): void
    {
        $this->db->run('INSERT INTO telemetry_state (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at', [$key, $value, gmdate('c')]);
    }

    private function time(string $key): ?int
    {
        $v = $this->get($key === 'last_success' ? 'last_success' : $key);
        return $v === null || $v === '' ? null : (int) $v;
    }
}
