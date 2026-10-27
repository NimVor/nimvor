<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;

/**
 * Server → Health: one page answering "is anything wrong?". It gathers the
 * checks that already exist elsewhere, each with a link to where it is
 * fixed:
 * - NimVor itself (`bin/whacp doctor`);
 * - resources (memory, swap, load);
 * - components;
 * - the security audit;
 * - updates;
 * - certificates;
 * - backups and failed jobs.
 * A part whose data is unavailable says so instead of failing the page.
 * It only reads.
 */
final class HealthService
{
    public const OK = 'ok';
    public const WARN = 'warn';
    public const FAIL = 'fail';
    /** The part could not be checked (agent unreachable, component missing). */
    public const UNKNOWN = 'unknown';

    public const SECTIONS = [
        'whacp' => ['NimVor and the server', 'heart-pulse', 'settings'],
        'resources' => ['Resources', 'speedometer', 'monitoring'],
        'components' => ['Components', 'puzzle', 'components'],
        'security' => ['Security', 'shield-check', 'security'],
        'updates' => ['Updates', 'box-seam', 'packages'],
        'certificates' => ['Certificates', 'shield-lock', 'ssl'],
        'activity' => ['Backups and tasks', 'archive', 'backups'],
    ];

    public function __construct(
        private readonly Doctor $doctor,
        private readonly AgentClient $agent,
        private readonly Connection $db,
        private readonly SettingsService $settings,
        private readonly ServerContext $server,
        private readonly DiscoveryService $discovery,
    ) {
    }

    /**
     * Every section with its checks; a check is
     * {status, label, detail, route?}.
     *
     * @return array<string, list<array{status: string, label: string, detail: string, route?: string}>>
     */
    public function run(): array
    {
        $out = [];
        foreach (array_keys(self::SECTIONS) as $s) {
            try {
                $out[$s] = $this->{'check' . ucfirst($s)}();
            } catch (AgentException $e) {
                $out[$s] = [['status' => self::UNKNOWN, 'label' => 'Not checked', 'detail' => $e->getMessage()]];
            }
        }
        return $out;
    }

    /**
     * The worst status and the counts of a result.
     *
     * @param array<string, list<array{status: string}>> $result
     * @return array{status: string, fail: int, warn: int, unknown: int, ok: int}
     */
    public static function summary(array $result): array
    {
        $n = ['fail' => 0, 'warn' => 0, 'unknown' => 0, 'ok' => 0];
        foreach ($result as $checks) {
            foreach ($checks as $c) {
                $n[$c['status']] = ($n[$c['status']] ?? 0) + 1;
            }
        }
        $status = $n['fail'] > 0 ? self::FAIL : ($n['warn'] > 0 ? self::WARN : ($n['unknown'] > 0 ? self::UNKNOWN : self::OK));
        return ['status' => $status] + $n;
    }

    /** @return list<array{status: string, label: string, detail: string, route?: string}> */
    private function checkWhacp(): array
    {
        $out = [];
        foreach ($this->doctor->run() as $r) {
            // Disks are shown under Resources.
            if (str_starts_with($r['check'], 'Disk ')) {
                continue;
            }
            $route = match (true) {
                $r['check'] === 'Backups' => 'backups',
                in_array($r['check'], ['Nginx', 'PHP-FPM', 'Database server', 'Redis'], true) => 'services',
                $r['check'] === 'Remote servers' => 'servers',
                str_starts_with($r['check'], "Let's Encrypt") => 'packages',
                default => null,
            };
            $out[] = ['status' => $r['status'], 'label' => $r['check'], 'detail' => $r['detail']] + ($route !== null ? ['route' => $route] : []);
        }
        return $out;
    }

    /** @return list<array{status: string, label: string, detail: string, route?: string}> */
    private function checkResources(): array
    {
        $m = (array) $this->agent->call('system.metrics');
        $out = [];
        foreach ((array) ($m['disks'] ?? []) as $d) {
            if (!is_array($d) || !isset($d['used_percent'])) {
                continue;
            }
            $pct = (float) $d['used_percent'];
            $alert = $this->settings->int('alert_disk_percent');
            $out[] = ['status' => $pct >= max(95, $alert) ? self::FAIL : ($pct >= min($alert, 85) ? self::WARN : self::OK), 'label' => 'Disk ' . ($d['mount_point'] ?? '?'),
                'detail' => sprintf('%.0f%% used, %s free', $pct, self::bytes((int) ($d['free_bytes'] ?? 0))), 'route' => 'monitoring'];
        }
        $mem = (array) ($m['memory'] ?? []);
        if (isset($mem['used_percent'])) {
            $pct = (float) $mem['used_percent'];
            $alert = $this->settings->int('alert_memory_percent');
            $out[] = ['status' => $pct >= $alert ? self::WARN : self::OK, 'label' => 'Memory',
                'detail' => sprintf('%.0f%% used of %s', $pct, self::bytes((int) ($mem['total_bytes'] ?? 0))), 'route' => 'monitoring'];
            $swapTotal = (int) ($mem['swap_total_bytes'] ?? 0);
            if ($swapTotal > 0) {
                $swap = 100 * (int) ($mem['swap_used_bytes'] ?? 0) / $swapTotal;
                $out[] = ['status' => $swap >= 80 ? self::WARN : self::OK, 'label' => 'Swap', 'detail' => sprintf('%.0f%% used of %s', $swap, self::bytes($swapTotal)), 'route' => 'monitoring'];
            }
        }
        $load = (array) ($m['load'] ?? []);
        if (isset($load['load1'])) {
            $cores = max(1, (int) ((((array) ((array) $this->agent->call('system.info'))['system'] ?? [])['cpu'] ?? [])['cores'] ?? 1));
            $per = (float) $load['load1'] / $cores;
            $out[] = ['status' => $per >= $this->settings->int('alert_load_per_core') ? self::WARN : self::OK, 'label' => 'Load',
                'detail' => sprintf('%.2f over the last minute on %d core(s)', (float) $load['load1'], $cores), 'route' => 'monitoring.processes'];
        }
        return $out;
    }

    /** @return list<array{status: string, label: string, detail: string, route?: string}> */
    private function checkComponents(): array
    {
        $components = (array) json_decode((string) json_encode($this->agent->call('components.status', [], 60)), true);
        $saved = $this->settings->string('components_expected');
        $expected = $saved !== '' ? explode(',', $saved) : array_column(array_filter($components, static fn (array $c): bool => !empty($c['installed'])), 'id');
        $out = [];
        foreach ($components as $c) {
            if (!in_array($c['id'], $expected, true)) {
                continue;
            }
            $todo = array_values(array_filter((array) $c['steps'], static fn (array $s): bool => ($s['status'] ?? '') === 'todo'));
            $detail = $todo === [] ? 'Ready' : implode('; ', array_map(static fn (array $s): string => (string) ($s['detail'] ?? $s['id']), $todo));
            // Not installed or not running is an outage; the rest needs attention.
            $down = !empty(array_filter($todo, static fn (array $s): bool => in_array($s['id'], ['installed', 'running'], true)));
            $out[] = ['status' => $todo === [] ? self::OK : ($down ? self::FAIL : self::WARN), 'label' => (string) $c['name'], 'detail' => $detail, 'route' => 'components'];
        }
        return $out;
    }

    /** @return list<array{status: string, label: string, detail: string, route?: string}> */
    private function checkSecurity(): array
    {
        $out = [];
        $warn = [];
        foreach ((array) $this->agent->call('security.audit', [], 120) as $c) {
            $c = (array) $c;
            $st = (string) ($c['status'] ?? '');
            if ($st === 'fail' || ($st === 'warn' && ($c['severity'] ?? '') === 'high')) {
                $out[] = ['status' => $st === 'fail' ? self::FAIL : self::WARN, 'label' => (string) ($c['title'] ?? $c['id'] ?? ''), 'detail' => (string) ($c['detail'] ?? ''), 'route' => 'security'];
            } elseif ($st === 'warn') {
                $warn[] = (string) ($c['title'] ?? $c['id'] ?? '');
            }
        }
        if ($warn !== []) {
            $out[] = ['status' => self::WARN, 'label' => count($warn) . ' recommendation(s)', 'detail' => implode(', ', array_slice($warn, 0, 8)) . (count($warn) > 8 ? ', …' : ''), 'route' => 'security'];
        }
        if ($out === []) {
            $out[] = ['status' => self::OK, 'label' => 'Security audit', 'detail' => 'No problem found.', 'route' => 'security'];
        }
        return $out;
    }

    /** @return list<array{status: string, label: string, detail: string, route?: string}> */
    private function checkUpdates(): array
    {
        $u = (array) $this->agent->call('packages.updates', [], 60);
        $count = (int) ($u['count'] ?? 0);
        $sec = (int) ($u['security'] ?? 0);
        $out = [['status' => $sec > 0 ? self::WARN : self::OK, 'label' => 'Package updates',
            'detail' => $count === 0 ? 'Up to date' : $count . ' update(s), ' . $sec . ' of them security update(s)', 'route' => 'packages']];
        if (!empty($u['index_stale'])) {
            $out[] = ['status' => self::WARN, 'label' => 'Package index', 'detail' => 'Not refreshed for a while' . (!empty($u['index_refreshed']) ? ' (last ' . substr((string) $u['index_refreshed'], 0, 16) . ')' : '') . ': updates may be missing from the list.', 'route' => 'packages'];
        }
        return $out;
    }

    /** @return list<array{status: string, label: string, detail: string, route?: string}> */
    private function checkCertificates(): array
    {
        $inv = $this->discovery->inventory(false, null);
        $days = $this->settings->int('ssl_warning_days');
        $sum = DiscoveryService::summary($inv['data'], $days);
        $expired = 0;
        foreach (DiscoveryService::websites($inv['data']) as $s) {
            if (isset($s['ssl']['days_remaining']) && (int) $s['ssl']['days_remaining'] < 0) {
                $expired++;
            }
        }
        return [[
            'status' => $expired > 0 ? self::FAIL : ($sum['expiring'] > 0 ? self::WARN : self::OK), 'label' => 'Website certificates',
            'detail' => $expired > 0 ? $expired . ' expired, ' . $sum['expiring'] . ' expiring within ' . $days . ' days' : ($sum['expiring'] > 0 ? $sum['expiring'] . ' expiring within ' . $days . ' days' : $sum['https'] . ' of ' . $sum['total'] . ' website(s) on HTTPS, none expiring soon'),
            'route' => 'ssl',
        ]];
    }

    /** @return list<array{status: string, label: string, detail: string, route?: string}> */
    private function checkActivity(): array
    {
        $since = gmdate('c', time() - 7 * 86400);
        $failedBackups = (int) $this->db->scalar("SELECT COUNT(*) FROM backups WHERE server_id = ? AND status = 'failed' AND created_at >= ?", [$this->server->id(), $since]);
        $failedJobs = (int) $this->db->scalar("SELECT COUNT(*) FROM agent_jobs WHERE server_id = ? AND status = 'failed' AND created_at >= ?", [$this->server->id(), gmdate('c', time() - 86400)]);
        return [
            ['status' => $failedBackups > 0 ? self::WARN : self::OK, 'label' => 'Failed backups (7 days)', 'detail' => $failedBackups === 0 ? 'None' : $failedBackups . ' failed', 'route' => 'backups'],
            ['status' => $failedJobs > 0 ? self::WARN : self::OK, 'label' => 'Failed tasks (24 hours)', 'detail' => $failedJobs === 0 ? 'None' : $failedJobs . ' failed: see Notifications', 'route' => 'notifications'],
        ];
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
}
