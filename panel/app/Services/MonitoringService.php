<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Support\Logger;

/**
 * Stores a metrics sample on every scheduler pass (kept 30 days),
 * evaluates alert rules and notifies an optional webhook when an alert
 * opens or resolves.
 *
 * Rules: disk, memory, load, service down, agent unreachable,
 * certificate expiring, backup failed.
 */
final class MonitoringService
{
    private const RETENTION_DAYS = 30;

    /** @var \Closure(string, string): bool|null test hook for webhook delivery */
    public ?\Closure $sender = null;

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly SettingsService $settings,
        private readonly ServerContext $server,
        private readonly Logger $log,
        private readonly ?PlanLimits $limits = null,
        private readonly ?\WhaCP\Mail\Mailer $mailer = null,
        private readonly ?\WhaCP\Mail\MailConfig $mailConfig = null,
        private readonly ?NotificationService $notifications = null,
    ) {
    }

    /**
     * Bytes sent this month by each website, read on from where the last
     * read of its access log stopped.
     */
    private function bandwidth(int $now): void
    {
        $rows = $this->db->fetchAll('SELECT w.id, w.domain, w.root, u.bw_month, u.bw_inode, u.bw_offset FROM websites w JOIN website_usage u ON u.website_id = w.id WHERE w.server_id = ?', [$this->server->id()]);
        if ($rows === []) {
            return;
        }
        $byDomain = array_column($rows, null, 'domain');
        try {
            $res = (array) $this->agent->call('sites.bandwidth', ['sites' => array_map(static fn (array $r): array => ['domain' => $r['domain'], 'root' => $r['root'], 'inode' => (int) $r['bw_inode'], 'offset' => (int) $r['bw_offset']], $rows)], 300);
        } catch (AgentException $e) {
            $this->log->warning('bandwidth measurement failed', ['error' => $e->getMessage()]);
            return;
        }
        $month = gmdate('Y-m', $now);
        foreach ($res as $t) {
            $t = (array) $t;
            $row = $byDomain[(string) ($t['domain'] ?? '')] ?? null;
            if ($row === null || !empty($t['not_found'])) {
                continue;
            }
            $add = (int) (((array) ($t['months'] ?? []))[$month] ?? 0);
            // A new month starts from zero.
            $this->db->run(
                'UPDATE website_usage SET bw_bytes = CASE WHEN bw_month = ? THEN bw_bytes + ? ELSE ? END, bw_month = ?, bw_inode = ?, bw_offset = ? WHERE website_id = ?',
                [$month, $add, $add, $month, (int) $t['inode'], (int) $t['offset'], (int) $row['id']]
            );
        }
    }

    /**
     * Measure website disk use (at most hourly) and return quota alerts.
     *
     * @return list<array{0: string, 1: string, 2: string, 3: string}>
     */
    private function quotas(int $now): array
    {
        $sites = $this->db->fetchAll('SELECT id, domain FROM websites WHERE server_id = ?', [$this->server->id()]);
        $last = (string) $this->db->scalar('SELECT MIN(measured_at) FROM website_usage u JOIN websites w ON w.id = u.website_id WHERE w.server_id = ?', [$this->server->id()]);
        $measured = (int) $this->db->scalar('SELECT COUNT(*) FROM website_usage u JOIN websites w ON w.id = u.website_id WHERE w.server_id = ?', [$this->server->id()]);
        if ($sites !== [] && ($measured < count($sites) || strtotime($last) < $now - 3600)) {
            $byDomain = array_column($sites, 'id', 'domain');
            try {
                $rows = (array) $this->agent->call('sites.usage', ['domains' => array_keys($byDomain)], 300);
            } catch (AgentException $e) {
                $this->log->warning('disk usage measurement failed', ['error' => $e->getMessage()]);
                $rows = [];
            }
            foreach ($rows as $r) {
                $r = (array) $r;
                if (!empty($r['not_found']) || !isset($byDomain[$r['domain']])) {
                    continue;
                }
                $this->db->run(
                    'INSERT INTO website_usage (website_id, bytes, files, measured_at) VALUES (?, ?, ?, ?) ON CONFLICT (website_id) DO UPDATE SET bytes = excluded.bytes, files = excluded.files, measured_at = excluded.measured_at',
                    [(int) $byDomain[$r['domain']], (int) $r['bytes'], (int) $r['files'], gmdate('c', $now)]
                );
            }
            $this->bandwidth($now);
        }
        $out = [];
        if ($this->limits === null) {
            return $out;
        }
        foreach ($this->db->fetchAll('SELECT u.id, u.username, p.bandwidth_mb FROM users u JOIN plans p ON p.id = u.plan_id WHERE p.bandwidth_mb > 0') as $u) {
            $used = $this->limits->count('bandwidth_mb', $this->limits->owners((int) $u['id']));
            if ($used > (int) $u['bandwidth_mb']) {
                $out[] = ['bandwidth', (string) $u['username'], 'warning', sprintf('%s used %d MB of a %d MB monthly bandwidth.', $u['username'], $used, (int) $u['bandwidth_mb'])];
            }
        }
        foreach ($this->db->fetchAll('SELECT u.id, u.username, p.disk_quota_mb FROM users u JOIN plans p ON p.id = u.plan_id WHERE p.disk_quota_mb > 0') as $u) {
            $owners = array_merge([(int) $u['id']], array_map('intval', array_column($this->db->fetchAll('SELECT id FROM users WHERE parent_id = ?', [(int) $u['id']]), 'id')));
            $used = $this->limits->count('disk_mb', $owners);
            if ($used > (int) $u['disk_quota_mb']) {
                $out[] = ['quota', (string) $u['username'], 'warning', sprintf('%s uses %d MB of a %d MB disk quota.', $u['username'], $used, (int) $u['disk_quota_mb'])];
            }
        }
        return $out;
    }

    /**
     * One monitoring pass. Returns log lines.
     *
     * @return list<string>
     */
    public function run(?int $now = null): array
    {
        $now ??= time();
        $firing = []; // key => [rule, subject, severity, message]
        try {
            $m = (array) $this->agent->call('system.metrics');
            $info = (array) $this->agent->call('system.info');
            $cores = max(1, (int) ($info['system']['cpu']['cores'] ?? 1));
            $disks = (array) ($m['disks'] ?? []);
            $maxDisk = 0.0;
            foreach ($disks as $d) {
                $d = (array) $d;
                $pct = (float) ($d['used_percent'] ?? 0);
                $maxDisk = max($maxDisk, $pct);
                if ($pct >= $this->settings->int('alert_disk_percent')) {
                    $mp = (string) ($d['mount_point'] ?? $d['mount'] ?? '?');
                    $firing['disk:' . $mp] = ['disk', $mp, $pct >= 98 ? 'critical' : 'warning', sprintf('Disk %s is %.0f%% full.', $mp, $pct)];
                }
            }
            $rx = $tx = 0.0;
            foreach ((array) ($m['network'] ?? []) as $n) {
                $rx += (float) ((array) $n)['rx_bytes_per_sec'];
                $tx += (float) ((array) $n)['tx_bytes_per_sec'];
            }
            $mem = (float) ($m['memory']['used_percent'] ?? 0);
            $load = (float) ($m['load']['load1'] ?? 0);
            $this->db->run(
                'INSERT INTO metric_samples (server_id, taken_at, cpu, mem, disk, load1, rx_bps, tx_bps) VALUES (?, ?, ?, ?, ?, ?, ?, ?)',
                [$this->server->id(), $now, (float) ($m['cpu_percent'] ?? 0), $mem, $maxDisk, $load, $rx, $tx]
            );
            $prevMem = (float) $this->db->scalar('SELECT mem FROM metric_samples WHERE server_id = ? AND taken_at < ? ORDER BY taken_at DESC LIMIT 1', [$this->server->id(), $now]);
            if ($mem >= $this->settings->int('alert_memory_percent') && $prevMem >= $this->settings->int('alert_memory_percent')) {
                $firing['memory'] = ['memory', '', 'warning', sprintf('Memory use is %.0f%%.', $mem)];
            }
            if ($load > $cores * $this->settings->int('alert_load_per_core')) {
                $firing['load'] = ['load', '', 'warning', sprintf('Load average %.2f on %d core(s).', $load, $cores)];
            }
            foreach ((array) $this->agent->call('services.status') as $svc) {
                $svc = (array) $svc;
                $enabled = ($svc['unit_file_state'] ?? '') === 'enabled';
                if ($enabled && in_array($svc['active_state'] ?? '', ['failed', 'inactive'], true)) {
                    $firing['service:' . $svc['unit']] = ['service', (string) $svc['unit'], 'critical', $svc['unit'] . ' is ' . $svc['active_state'] . '.'];
                }
            }
            foreach ($this->quotas($now) as $q) {
                $firing[] = $q;
            }
            $warnDays = $this->settings->int('ssl_warning_days');
            foreach ((array) $this->agent->call('discovery.certificates') as $c) {
                $cert = (array) ((array) $c)['certificate'];
                $days = (int) ($cert['days_remaining'] ?? 999);
                if ($days <= $warnDays) {
                    $name = (string) ((array) $c)['name'];
                    $firing['certificate:' . $name] = ['certificate', $name, $days <= 3 ? 'critical' : 'warning', $days < 0 ? "Certificate {$name} has expired." : "Certificate {$name} expires in {$days} day(s)."];
                }
            }
        } catch (AgentException $e) {
            $firing['agent'] = ['agent', '', 'critical', 'The WhaCP agent is not reachable: ' . $e->getMessage()];
            // Nothing could be measured: keep agent-based alerts open instead
            // of reporting them as resolved.
            foreach ($this->db->fetchAll("SELECT * FROM alerts WHERE server_id = ? AND resolved_at IS NULL AND rule IN ('disk', 'memory', 'load', 'service', 'certificate', 'quota', 'bandwidth')", [$this->server->id()]) as $a) {
                $firing[] = [(string) $a['rule'], (string) $a['subject'], (string) $a['severity'], (string) $a['message']];
            }
        }
        foreach ($this->db->fetchAll("SELECT domain, kind, error FROM backups WHERE server_id = ? AND status = 'failed' AND created_at > ?", [$this->server->id(), gmdate('c', $now - 86400)]) as $b) {
            $what = $b['kind'] === 'server' ? 'server configuration' : (string) $b['domain'];
            $firing['backup:' . $what] = ['backup', $what, 'warning', "Backup of {$what} failed: " . mb_substr((string) $b['error'], 0, 150)];
        }
        $log = $this->reconcile($firing, $now);
        $this->db->run('DELETE FROM metric_samples WHERE taken_at < ?', [$now - self::RETENTION_DAYS * 86400]);
        $this->db->run('DELETE FROM alerts WHERE resolved_at IS NOT NULL AND resolved_at < ?', [gmdate('c', $now - 90 * 86400)]);
        return $log;
    }

    /**
     * Open new alerts, refresh firing ones and resolve the rest.
     *
     * @param array<string, array{0: string, 1: string, 2: string, 3: string}> $firing
     * @return list<string>
     */
    private function reconcile(array $firing, int $now): array
    {
        $log = [];
        $ts = gmdate('c', $now);
        $open = [];
        foreach ($this->db->fetchAll('SELECT * FROM alerts WHERE server_id = ? AND resolved_at IS NULL', [$this->server->id()]) as $a) {
            $open[$a['rule'] . ($a['subject'] !== '' ? ':' . $a['subject'] : '')] = $a;
        }
        // Key alerts by rule and subject (the array keys above are only for de-duplication).
        $byKey = [];
        foreach ($firing as [$rule, $subject, $severity, $message]) {
            $byKey[$rule . ($subject !== '' ? ':' . $subject : '')] = [$rule, $subject, $severity, $message];
        }
        $firing = $byKey;
        foreach ($firing as $key => [$rule, $subject, $severity, $message]) {
            if (isset($open[$key])) {
                $this->db->run('UPDATE alerts SET last_seen = ?, message = ?, severity = ? WHERE id = ?', [$ts, $message, $severity, (int) $open[$key]['id']]);
                continue;
            }
            $this->db->run('INSERT INTO alerts (server_id, rule, subject, severity, message, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)', [$this->server->id(), $rule, $subject, $severity, $message, $ts, $ts]);
            $this->notifyAlert($this->db->lastInsertId(), $rule, $subject, $severity === 'critical' ? 'error' : 'warning', $message);
            $log[] = 'Alert: ' . $message;
            $this->notify(($severity === 'critical' ? '🔴 ' : '🟠 ') . $message);
        }
        foreach ($open as $key => $a) {
            if (!isset($firing[$key])) {
                $this->db->run('UPDATE alerts SET resolved_at = ? WHERE id = ?', [$ts, (int) $a['id']]);
                $this->notifyAlert((int) $a['id'], (string) $a['rule'], (string) $a['subject'], 'success', 'Resolved: ' . $a['message']);
                $log[] = 'Resolved: ' . $a['message'];
                $this->notify('✅ Resolved: ' . $a['message']);
            }
        }
        return $log;
    }

    /**
     * Record an alert (or its resolution) as a notification. Alerts about a
     * website (certificate, backup) or a user (quota) are shown to its
     * owner as well; the rest to administrators only.
     */
    private function notifyAlert(int $alertId, string $rule, string $subject, string $severity, string $message): void
    {
        if ($this->notifications === null) {
            return;
        }
        $opts = ['link' => '/monitoring'];
        if (($rule === 'certificate' || $rule === 'backup') && $subject !== '') {
            $site = $this->db->fetch('SELECT id FROM websites WHERE server_id = ? AND domain = ? COLLATE NOCASE', [$this->server->id(), $subject]);
            if ($site !== null) {
                $opts = ['website_id' => (int) $site['id'], 'link' => '/sites/' . (int) $site['id'] . ($rule === 'backup' ? '/backups' : '')];
            }
        } elseif (($rule === 'quota' || $rule === 'bandwidth') && $subject !== '') {
            $owner = $this->db->scalar('SELECT id FROM users WHERE username = ? COLLATE NOCASE', [$subject]);
            if ($owner !== null && $owner !== false) {
                $opts = ['owner_id' => (int) $owner, 'link' => ''];
            }
        }
        $this->notifications->notify('alert:' . $alertId . ':' . ($severity === 'success' ? 'resolved' : 'open'), 'alert', $severity, $message, $opts);
    }

    private function email(string $host, string $text): void
    {
        if ($this->mailer === null || $this->mailConfig === null) {
            return;
        }
        $to = $this->mailConfig->alertRecipients();
        if ($to === [] || !$this->mailer->configured()) {
            return;
        }
        try {
            $this->mailer->send($to, "[WhaCP {$host}] " . mb_substr($text, 0, 120), $text . "\n\n-- \nWhaCP on {$host}. Change alert settings under Settings.");
        } catch (\WhaCP\Mail\MailException) {
            // Logged by the mailer; the alert itself is stored and shown.
        }
    }

    private function notify(string $text): void
    {
        $host = gethostname() ?: 'server';
        $this->email($host, $text);
        $url = $this->settings->string('alert_webhook_url');
        if ($url === '') {
            return;
        }
        $body = (string) json_encode(['text' => "[WhaCP {$host}] " . $text], JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES);
        if ($this->sender !== null) {
            ($this->sender)($url, $body);
            return;
        }
        $ctx = stream_context_create(['http' => [
            'method' => 'POST', 'header' => "Content-Type: application/json\r\nUser-Agent: WhaCP\r\n", 'content' => $body,
            'timeout' => 5, 'ignore_errors' => true, 'follow_location' => 0,
        ]]);
        if (@file_get_contents($url, false, $ctx) === false) {
            $this->log->warning('alert webhook failed', ['url' => parse_url($url, PHP_URL_HOST)]);
        }
    }

    /** @return list<array<string, mixed>> */
    public function openAlerts(): array
    {
        return $this->db->fetchAll("SELECT * FROM alerts WHERE server_id = ? AND resolved_at IS NULL ORDER BY CASE severity WHEN 'critical' THEN 0 ELSE 1 END, first_seen", [$this->server->id()]);
    }

    /** @return list<array<string, mixed>> */
    public function recentAlerts(int $limit = 50): array
    {
        return $this->db->fetchAll('SELECT * FROM alerts WHERE server_id = ? AND resolved_at IS NOT NULL ORDER BY resolved_at DESC LIMIT ?', [$this->server->id(), $limit]);
    }

    /**
     * Samples averaged into at most $buckets points for a chart.
     *
     * @return list<array{t: int, cpu: float, mem: float, disk: float, load1: float, rx: float, tx: float}>
     */
    public function series(int $seconds, int $buckets = 120, ?int $now = null): array
    {
        $now ??= time();
        $from = $now - $seconds;
        $size = max(1, intdiv($seconds, $buckets));
        $rows = $this->db->fetchAll(
            'SELECT (taken_at - :from) / :size AS b, MIN(taken_at) AS t, AVG(cpu) AS cpu, AVG(mem) AS mem, MAX(disk) AS disk, AVG(load1) AS load1, AVG(rx_bps) AS rx, AVG(tx_bps) AS tx
             FROM metric_samples WHERE server_id = :server AND taken_at >= :from GROUP BY b ORDER BY b',
            ['from' => $from, 'size' => $size, 'server' => $this->server->id()]
        );
        return array_map(static fn (array $r): array => [
            't' => (int) $r['t'], 'cpu' => (float) $r['cpu'], 'mem' => (float) $r['mem'], 'disk' => (float) $r['disk'],
            'load1' => (float) $r['load1'], 'rx' => (float) $r['rx'], 'tx' => (float) $r['tx'],
        ], $rows);
    }
}
