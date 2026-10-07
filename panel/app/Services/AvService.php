<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Support\UserFacingException;

/**
 * Malware scans with ClamAV. The agent scans a website as the website's
 * own account (a job) and, when quarantine is on, moves infected files into
 * the website's .whacp-quarantine folder; the panel records each scan and
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
    public function scan(array $site, ?int $userId, string $source = 'manual'): string
    {
        if ($this->running((int) $site['id']) !== null) {
            throw new UserFacingException('A scan of ' . $site['domain'] . ' is running.');
        }
        $quarantine = $this->settings->bool('av_quarantine');
        $res = (array) $this->agent->call('av.scan', [
            'domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'], 'quarantine' => $quarantine,
        ]);
        $job = (string) ($res['job_id'] ?? '');
        $this->db->run(
            'INSERT INTO av_scans (server_id, website_id, job_id, source, user_id, status, quarantine, started_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)',
            [$this->server->id(), (int) $site['id'], $job, $source, $userId, 'running', $quarantine ? 1 : 0, gmdate('c')]
        );
        if ($source === 'manual') {
            $this->audit->record('av.scan', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain']], $userId);
        }
        return $job;
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
