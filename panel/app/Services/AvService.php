<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;
use NimVor\Support\UserFacingException;

/**
 * Malware scans with ClamAV. The agent scans a website as the website's
 * own account (a job) and, when quarantine is on, moves infected files into
 * the website's .nimvor-quarantine folder; the panel records each scan and
 * notifies the website's owner of what was found.
 */
final class AvService
{
    /** Scheduled scans start at night (server time), a few per scheduler pass. */
    private const NIGHT = [1, 6];
    private const PER_PASS = 3;

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
        private readonly SettingsService $settings,
        private readonly NotificationService $notifications,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @return array{installed: bool, version: string, freshclam: bool, updater_log: string} */
    public function status(): array
    {
        $s = (array) $this->agent->call('av.status');
        return ['installed' => !empty($s['installed']), 'version' => (string) ($s['version'] ?? ''), 'freshclam' => !empty($s['freshclam']), 'updater_log' => (string) ($s['updater_log'] ?? '')];
    }

    public function updateSignatures(?int $userId): void
    {
        $this->agent->call('av.update');
        $this->audit->record('av.update', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, [], $userId);
    }

    /**
     * Start a scan of a website.
     *
     * @param array<string, mixed> $site
     * @return string the agent job id
     */
    /**
     * Start a scan. With $paths (relative to the website) only those files
     * or folders are scanned — used for scan-on-upload and scan-on-extract,
     * which always quarantine so an infected upload cannot be served. A
     * whole-site scan ($paths null) is exclusive per website; targeted scans
     * are not and may run alongside others.
     *
     * @param array<string, mixed> $site
     * @param list<string>|null    $paths
     */
    public function scan(array $site, ?int $userId, string $source = 'manual', ?array $paths = null): string
    {
        if ($paths === null && $this->running((int) $site['id']) !== null) {
            throw new UserFacingException('A scan of ' . $site['domain'] . ' is running.');
        }
        // Targeted upload/extract scans always quarantine; whole-site scans
        // follow the administrator's setting.
        $quarantine = $paths !== null ? true : $this->settings->bool('av_quarantine');
        $params = ['domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'], 'quarantine' => $quarantine];
        if ($paths !== null) {
            $params['paths'] = array_values($paths);
        }
        $res = (array) $this->agent->call('av.scan', $params);
        $job = (string) ($res['job_id'] ?? '');
        $this->db->run(
            'INSERT INTO av_scans (server_id, website_id, job_id, source, user_id, status, quarantine, paths, started_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)',
            [$this->server->id(), (int) $site['id'], $job, $source, $userId, 'running', $quarantine ? 1 : 0, json_encode(array_values($paths ?? []), JSON_UNESCAPED_SLASHES), gmdate('c')]
        );
        if ($source === 'manual') {
            $this->audit->record('av.scan', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain']], $userId);
        }
        return $job;
    }

    /**
     * Scan files just uploaded or extracted, when the policy is on and
     * ClamAV is installed. Returns the scan job id, or '' when skipped
     * (scanner unavailable, policy off, or the file is larger than the
     * configured maximum). Never throws: a scan problem must not fail the
     * upload, but an infected file is still quarantined by the scan.
     *
     * @param array<string, mixed> $site
     * @param list<string>         $paths
     */
    public function scanUploaded(array $site, ?int $userId, string $source, array $paths, int $bytes = 0): string
    {
        $setting = $source === 'extract' ? 'av_scan_on_extract' : 'av_scan_on_upload';
        if ($paths === [] || !$this->settings->bool($setting)) {
            return '';
        }
        $maxMb = $this->settings->int('av_scan_max_mb');
        if ($source !== 'extract' && $maxMb > 0 && $bytes > $maxMb * 1048576) {
            return '';
        }
        try {
            if (empty($this->status()['installed'])) {
                return '';
            }
            return $this->scan($site, $userId, $source, $paths);
        } catch (\Throwable) {
            return '';
        }
    }

    /**
     * The files currently in a website's quarantine (moved there by a scan):
     * where each came from, its size and when it was quarantined.
     *
     * @param array<string, mixed> $site
     * @return list<array<string, mixed>>
     */
    public function quarantined(array $site): array
    {
        $res = (array) $this->agent->call('av.quarantine_list', ['domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root']]);
        $items = [];
        foreach ((array) ($res['items'] ?? []) as $i) {
            $items[] = (array) $i;
        }
        return $items;
    }

    /**
     * Release a file from a website's quarantine. The agent re-scans it first
     * and refuses if it is still infected or its original location is taken.
     * Returns the restored path (relative to the website).
     *
     * @param array<string, mixed> $site
     */
    public function release(array $site, ?int $userId, string $path): string
    {
        $res = (array) $this->agent->call('av.release', ['domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'], 'path' => $path]);
        $restored = (string) ($res['path'] ?? $path);
        $this->audit->record('av.release', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'quarantine_path' => $path, 'restored' => $restored], $userId);
        return $restored;
    }

    /** @return array<string, mixed>|null the website's running scan */
    public function running(int $siteId): ?array
    {
        return $this->db->fetch("SELECT * FROM av_scans WHERE website_id = ? AND status = 'running' ORDER BY id DESC LIMIT 1", [$siteId]);
    }

    /** @return list<array<string, mixed>> a website's scans, newest first */
    public function history(int $siteId, int $limit = 10): array
    {
        return array_map(self::decode(...), $this->db->fetchAll('SELECT * FROM av_scans WHERE website_id = ? ORDER BY id DESC LIMIT ?', [$siteId, $limit]));
    }

    /** @return list<array<string, mixed>> this server's latest scans, with their website */
    public function recent(int $limit = 50): array
    {
        return array_map(self::decode(...), $this->db->fetchAll(
            'SELECT s.*, w.domain, u.username AS owner_name FROM av_scans s JOIN websites w ON w.id = s.website_id LEFT JOIN users u ON u.id = w.owner_id
             WHERE s.server_id = ? ORDER BY s.id DESC LIMIT ?',
            [$this->server->id(), $limit]
        ));
    }

    /**
     * @param array<string, mixed> $row
     * @return array<string, mixed>
     */
    private static function decode(array $row): array
    {
        $f = json_decode((string) $row['findings'], true);
        $row['findings'] = is_array($f) ? $f : [];
        return $row;
    }

    /**
     * Record the results of this server's finished scans (and tell the
     * website's owner about infected files).
     *
     * @return list<string> log lines
     */
    public function refresh(): array
    {
        $log = [];
        foreach ($this->db->fetchAll("SELECT s.*, w.domain, w.owner_id FROM av_scans s JOIN websites w ON w.id = s.website_id WHERE s.server_id = ? AND s.status = 'running'", [$this->server->id()]) as $scan) {
            try {
                $job = (array) $this->agent->call('jobs.get', ['id' => $scan['job_id']]);
            } catch (AgentException $e) {
                if ($e->errorCode() === 'not_found') {
                    $this->finish($scan, 'failed', [], 'The scan was interrupted (the agent restarted).');
                    $log[] = "Malware scan of {$scan['domain']}: interrupted.";
                }
                continue;
            }
            $status = (string) ($job['status'] ?? '');
            if ($status === 'failed') {
                $this->finish($scan, 'failed', [], (string) ($job['error'] ?? 'The scan failed.'));
                $log[] = "Malware scan of {$scan['domain']}: failed.";
            } elseif ($status === 'succeeded') {
                $result = (array) ($job['result'] ?? []);
                $infected = $this->finish($scan, 'done', $result, '');
                $log[] = "Malware scan of {$scan['domain']}: " . ($infected > 0 ? "{$infected} infected file(s)." : 'clean.');
            }
        }
        return $log;
    }

    /**
     * @param array<string, mixed> $scan
     * @param array<string, mixed> $result
     * @return int infected files
     */
    private function finish(array $scan, string $status, array $result, string $error): int
    {
        $findings = [];
        foreach ((array) ($result['infected'] ?? []) as $f) {
            $f = (array) $f;
            $path = (string) ($f['path'] ?? '');
            if ($path === '' || count($findings) >= 1000) {
                continue;
            }
            $findings[] = ['path' => mb_substr($path, 0, 500), 'signature' => mb_substr((string) ($f['signature'] ?? ''), 0, 200), 'quarantined' => !empty($f['quarantined'])];
        }
        $infected = count((array) ($result['infected'] ?? []));
        if ($status === 'done' && $infected > 0) {
            $status = 'infected';
        } elseif ($status === 'done') {
            $status = 'clean';
        }
        $updated = $this->db->run(
            "UPDATE av_scans SET status = ?, scanned = ?, infected = ?, findings = ?, error = ?, finished_at = ? WHERE id = ? AND status = 'running'",
            [$status, (int) ($result['scanned'] ?? 0), $infected, json_encode($findings, JSON_UNESCAPED_SLASHES), mb_substr($error, 0, 1000), gmdate('c'), (int) $scan['id']]
        )->rowCount();
        if ($updated === 0) {
            return $infected;
        }
        $opts = ['website_id' => (int) $scan['website_id'], 'link' => '/sites/' . (int) $scan['website_id'] . '/malware'];
        if ($status === 'infected') {
            $moved = count(array_filter($findings, static fn (array $f): bool => $f['quarantined']));
            $this->notifications->notify('av:' . $scan['id'], 'security', 'warning', "Malware found on {$scan['domain']}", $opts + [
                'body' => "{$infected} infected file(s)" . ($moved > 0 ? ", {$moved} moved to quarantine" : '') . ': ' . implode(', ', array_slice(array_column($findings, 'path'), 0, 5)) . ($infected > 5 ? ', …' : '') . '.',
            ]);
        } elseif ($status === 'failed' && $scan['source'] !== 'manual') {
            // (A manual scan's job failure is reported with its log.)
            $this->notifications->notify('av:' . $scan['id'], 'security', 'error', "Malware scan of {$scan['domain']} failed", $opts + ['body' => $error]);
        } elseif ($scan['source'] === 'manual') {
            $this->notifications->notify('av:' . $scan['id'], 'security', 'success', "{$scan['domain']} is clean", $opts + ['body' => 'The malware scan found nothing (' . (int) ($result['scanned'] ?? 0) . ' files).']);
        }
        return $infected;
    }

    /**
     * Scheduler pass: record finished scans, then start the scans that are
     * due (every av_scan_days days, at night, a few at a time).
     *
     * @return list<string> log lines
     */
    public function run(?int $now = null): array
    {
        $now ??= time();
        $log = [];
        try {
            $log = $this->refresh();
        } catch (AgentException $e) {
            return ['Malware scans not checked: ' . $e->getMessage()];
        }
        $days = $this->settings->int('av_scan_days');
        $hour = (int) date('G', $now);
        if ($days <= 0 || $hour < self::NIGHT[0] || $hour >= self::NIGHT[1]) {
            return $log;
        }
        // Websites whose last scan started before the interval; a subdomain
        // inside its parent's folder is scanned with the parent.
        $due = $this->db->fetchAll(
            "SELECT w.* FROM websites w LEFT JOIN websites p ON p.id = w.parent_id
             WHERE w.server_id = ? AND w.status = 'active'
               AND (p.id IS NULL OR substr(w.root, 1, length(p.root) + 1) <> p.root || '/')
               AND NOT EXISTS (SELECT 1 FROM av_scans s WHERE s.website_id = w.id AND (s.status = 'running' OR s.started_at > ?))
             ORDER BY (SELECT MAX(s.id) FROM av_scans s WHERE s.website_id = w.id) IS NOT NULL, w.id LIMIT ?",
            [$this->server->id(), gmdate('c', $now - $days * 86400 + 3600), self::PER_PASS]
        );
        foreach ($due as $site) {
            try {
                $this->scan($site, null, 'scheduled');
                $log[] = "Malware scan of {$site['domain']} started.";
            } catch (UserFacingException $e) {
                if (str_contains($e->getMessage(), 'not installed')) {
                    return $log;
                }
                $log[] = "Malware scan of {$site['domain']} not started: " . $e->getMessage();
            }
        }
        return $log;
    }
}
