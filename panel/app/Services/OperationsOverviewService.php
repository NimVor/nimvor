<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;

/**
 * What the dashboard's backup, update, DNS and mail, deployment and WordPress
 * widgets show. Backups, deployments and WordPress come from the panel's own
 * records (no agent call); updates, DNS and mail from existing read-only agent
 * actions. Something that cannot be measured is null, shown as N/A.
 */
final class OperationsOverviewService
{
    public function __construct(
        private readonly Connection $db,
        private readonly ServerContext $server,
        private readonly AgentClient $agent,
    ) {
    }

    /** @return array<string, mixed>|null */
    private function read(string $action, int $timeout = 30): ?array
    {
        try {
            return (array) json_decode((string) json_encode($this->agent->call($action, [], $timeout)), true);
        } catch (AgentException) {
            return null;
        }
    }

    /**
     * Backup health: enabled websites without a completed backup in the last
     * seven days, the latest failure and why, schedules, copies kept off this
     * server, encryption. Whether a backup can be restored is not recorded
     * anywhere, so it is not claimed.
     *
     * @return array<string, mixed>
     */
    public function backups(): array
    {
        $sid = $this->server->id();
        $week = gmdate('c', time() - 7 * 86400);
        $sites = (int) $this->db->scalar('SELECT COUNT(*) FROM websites WHERE server_id = ? AND enabled = 1', [$sid]);
        $covered = (int) $this->db->scalar(
            "SELECT COUNT(DISTINCT b.website_id) FROM backups b JOIN websites w ON w.id = b.website_id AND w.enabled = 1
             WHERE b.server_id = ? AND b.status = 'completed' AND b.created_at >= ?",
            [$sid, $week]
        );
        $stale = $this->db->fetchAll(
            "SELECT w.domain FROM websites w WHERE w.server_id = ? AND w.enabled = 1
             AND NOT EXISTS (SELECT 1 FROM backups b WHERE b.website_id = w.id AND b.status = 'completed' AND b.created_at >= ?)
             ORDER BY w.domain LIMIT 6",
            [$sid, $week]
        );
        $total = (int) $this->db->scalar("SELECT COUNT(*) FROM backups WHERE server_id = ? AND status = 'completed'", [$sid]);
        $failed = $this->db->fetch("SELECT domain, error, created_at FROM backups WHERE server_id = ? AND status = 'failed' ORDER BY id DESC LIMIT 1", [$sid]);
        $last = $this->db->scalar("SELECT MAX(created_at) FROM backups WHERE server_id = ? AND status = 'completed'", [$sid]);
        $sched = $this->db->fetch(
            "SELECT COUNT(*) AS n, SUM(CASE WHEN enabled = 1 THEN 1 ELSE 0 END) AS on_n, SUM(CASE WHEN enabled = 1 AND last_status = 'failed' THEN 1 ELSE 0 END) AS failing FROM backup_schedules WHERE server_id = ?",
            [$sid]
        );
        $size = (int) $this->db->scalar("SELECT COALESCE(SUM(size), 0) FROM backups WHERE server_id = ? AND status = 'completed'", [$sid]);
        $offsite = (int) $this->db->scalar("SELECT COUNT(*) FROM backups WHERE server_id = ? AND status = 'completed' AND storage_id IS NOT NULL", [$sid]);
        $enc = (int) $this->db->scalar("SELECT COUNT(*) FROM backups WHERE server_id = ? AND status = 'completed' AND encrypted = 1", [$sid]);
        return [
            'sites' => $sites,
            'covered' => $covered,
            'uncovered' => array_map(static fn (array $r): string => (string) $r['domain'], $stale),
            'completed' => $total,
            'last' => $last === null || $last === false ? null : (string) $last,
            'failed' => $failed,
            'schedules' => (int) ($sched['n'] ?? 0),
            'schedules_on' => (int) ($sched['on_n'] ?? 0),
            'schedules_failing' => (int) ($sched['failing'] ?? 0),
            'size' => $size,
            'offsite_pct' => $total > 0 ? (int) round(100 * $offsite / $total) : null,
            'encrypted_pct' => $total > 0 ? (int) round(100 * $enc / $total) : null,
            '_state' => $sites === 0 && $total === 0 ? 'empty' : 'ok',
        ];
    }

    /**
     * Package updates (count, security, stale index) and whether a restart is
     * needed to finish installing them.
     *
     * @return array<string, mixed>
     */
    public function updates(): array
    {
        $u = $this->read('packages.updates', 90);
        if ($u === null) {
            throw new AgentException('Updates unavailable');
        }
        $sys = $this->read('server.settings');
        return [
            'count' => isset($u['count']) ? (int) $u['count'] : null,
            'security' => isset($u['security']) ? (int) $u['security'] : null,
            'index_stale' => !empty($u['index_stale']),
            'index_refreshed' => (string) ($u['index_refreshed'] ?? ''),
            'reboot_required' => $sys === null ? null : !empty($sys['reboot_required']),
            'reboot_by' => $sys === null ? [] : array_slice(array_map('strval', (array) ($sys['reboot_required_by'] ?? [])), 0, 4),
        ];
    }

    /**
     * The nameserver (running, zones) and the mail services and queue.
     *
     * @return array<string, mixed>
     */
    public function dnsMail(): array
    {
        $dns = $this->read('dns.status');
        $mail = $this->read('mail.server_status', 60);
        $queue = $mail === null ? null : $this->read('mail.queue', 60);
        if ($dns === null && $mail === null) {
            throw new AgentException('DNS and mail status unavailable');
        }
        $sid = $this->server->id();
        $services = [];
        foreach ((array) ($mail['services'] ?? []) as $s) {
            $s = (array) $s;
            if (!empty($s['installed'])) {
                $services[] = ['name' => (string) $s['name'], 'active' => !empty($s['active'])];
            }
        }
        $counts = (array) ($queue['counts'] ?? []);
        return [
            'dns' => $dns === null ? null : [
                'label' => (string) ($dns['backend_label'] ?? ''),
                'installed' => !empty($dns['installed']),
                'running' => !empty($dns['running']),
                'zones' => count((array) ($dns['zones'] ?? [])),
                'signed' => count((array) ($dns['signed'] ?? [])),
            ],
            'mail' => $mail === null ? null : ['services' => $services],
            'queue' => $queue === null ? null : [
                'total' => (int) ($queue['total'] ?? 0),
                'deferred' => (int) ($counts['deferred'] ?? 0),
                'hold' => (int) ($counts['hold'] ?? 0),
            ],
            'mail_domains' => (int) $this->db->scalar('SELECT COUNT(*) FROM mail_domains WHERE server_id = ?', [$sid]),
            'zones_in_panel' => (int) $this->db->scalar('SELECT COUNT(*) FROM dns_zones WHERE server_id = ?', [$sid]),
        ];
    }

    /**
     * Deployments in the last seven days (from the panel's records).
     *
     * @return array<string, mixed>
     */
    public function deployments(): array
    {
        $sid = $this->server->id();
        $week = gmdate('c', time() - 7 * 86400);
        $row = $this->db->fetch(
            "SELECT COUNT(*) AS n, SUM(CASE WHEN d.status = 'failed' THEN 1 ELSE 0 END) AS failed, SUM(CASE WHEN d.status = 'running' THEN 1 ELSE 0 END) AS running
             FROM deployments d JOIN websites w ON w.id = d.website_id WHERE w.server_id = ? AND d.created_at >= ?",
            [$sid, $week]
        );
        $fail = $this->db->fetch(
            "SELECT w.domain, d.error, d.created_at FROM deployments d JOIN websites w ON w.id = d.website_id
             WHERE w.server_id = ? AND d.status = 'failed' ORDER BY d.id DESC LIMIT 1",
            [$sid]
        );
        $last = $this->db->fetch(
            'SELECT w.domain, d.status, d.created_at FROM deployments d JOIN websites w ON w.id = d.website_id WHERE w.server_id = ? ORDER BY d.id DESC LIMIT 1',
            [$sid]
        );
        $n = (int) ($row['n'] ?? 0);
        return [
            'week' => $n,
            'failed' => (int) ($row['failed'] ?? 0),
            'running' => (int) ($row['running'] ?? 0),
            'last' => $last,
            'last_failure' => $fail,
            '_state' => $last === null ? 'empty' : 'ok',
        ];
    }

    /**
     * WordPress installations found on the server: out-of-date core, plugin and
     * theme updates, open issues, from the last scan.
     *
     * @return array<string, mixed>
     */
    public function wordpress(): array
    {
        $sid = $this->server->id();
        $rows = $this->db->fetchAll('SELECT i.*, w.domain FROM wordpress_installs i JOIN websites w ON w.id = i.website_id WHERE i.server_id = ? ORDER BY w.domain', [$sid]);
        $outdated = [];
        $plugins = $themes = $issues = 0;
        $oldest = null;
        foreach ($rows as $r) {
            $latest = (string) $r['latest'];
            if ($latest !== '' && $r['version'] !== '' && version_compare((string) $r['version'], $latest, '<')) {
                $outdated[] = ['domain' => (string) $r['domain'], 'version' => (string) $r['version'], 'latest' => $latest];
            }
            $plugins += (int) $r['plugin_updates'];
            $themes += (int) $r['theme_updates'];
            $issues += (int) $r['issues'];
            $oldest = $oldest === null || (string) $r['scanned_at'] < $oldest ? (string) $r['scanned_at'] : $oldest;
        }
        return [
            'installs' => count($rows),
            'outdated' => array_slice($outdated, 0, 5),
            'outdated_total' => count($outdated),
            'plugin_updates' => $plugins,
            'theme_updates' => $themes,
            'issues' => $issues,
            'scanned_at' => $oldest,
            '_state' => $rows === [] ? 'empty' : 'ok',
        ];
    }
}
