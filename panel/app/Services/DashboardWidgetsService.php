<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Dashboard\WidgetCache;
use NimVor\Dashboard\WidgetRegistry;
use NimVor\Database\Connection;

/**
 * Numbers for the administrator dashboard's widgets, from the panel's own
 * records and one quick agent call. Each method fails soft: a widget with no
 * data shows an empty state, it never breaks the dashboard.
 */
final class DashboardWidgetsService
{
    public function __construct(
        private readonly Connection $db,
        private readonly ServerDiskService $disk,
        private readonly ServerContext $server,
        private readonly ?AgentClient $agent = null,
        private readonly ?WidgetCache $cache = null,
        private readonly ?WidgetRegistry $registry = null,
        private readonly ?SitesHealthService $siteHealth = null,
        private readonly ?DiscoveryService $discovery = null,
        private readonly ?SettingsService $settings = null,
        private readonly ?WebsiteMonitor $monitor = null,
        private readonly ?SecurityOverviewService $security = null,
        private readonly ?ServiceOverviewService $services = null,
        private readonly ?OperationsOverviewService $ops = null,
        private readonly ?CapacityService $capacity = null,
        private readonly ?MonitoringService $monitoring = null,
    ) {
    }

    /**
     * What one widget shows, kept for the widget's own time (WidgetRegistry).
     * The state says how to draw it: ok, empty, unavailable (the source cannot
     * answer right now) or error. A number that could not be measured is null,
     * never a made-up zero.
     *
     * @param array<string, mixed> $user
     * @return array{state: string, data: array<string, mixed>, at: int}
     */
    public function widget(string $id, array $user): array
    {
        $def = $this->registry?->find($id);
        if ($def === null || $this->cache === null) {
            return ['state' => 'error', 'data' => [], 'at' => time()];
        }
        try {
            $r = $this->cache->remember($id, 'server', $def->ttl, fn (): array => match ($id) {
                'tile_cpu', 'tile_memory', 'tile_disk', 'tile_load' => ['_state' => 'ok'],
                'software' => $this->software(),
                'websites_summary' => $this->websitesSummary($user),
                'request_trends' => $this->requestTrends(),
                'attention' => $this->attention($user),
                'disk' => $this->disk(),
                'backups' => $this->backups() + ['attention' => (int) ($this->siteHealth?->overview($user)['counts']['backup'] ?? 0)],
                'top_processes' => $this->topProcesses(),
                'website_health' => $this->websiteHealth(),
                'security_overview' => ($this->security ?? throw new AgentException('Security status unavailable'))->security(),
                'alert_center' => $this->alertCenter(),
                'performance_history' => $this->performanceHistory(),
                'website_overview' => $this->websiteOverview(),
                'services_overview' => $this->servicesOverview(),
                'capacity_forecast' => ($this->capacity ?? throw new \InvalidArgumentException('Capacity unavailable'))->forecast(),
                'traffic_overview' => ($this->capacity ?? throw new \InvalidArgumentException('Traffic unavailable'))->traffic(),
                'backup_health' => ($this->ops ?? throw new AgentException('Backups unavailable'))->backups(),
                'system_updates' => ($this->ops ?? throw new AgentException('Updates unavailable'))->updates(),
                'dns_mail' => ($this->ops ?? throw new AgentException('DNS and mail unavailable'))->dnsMail(),
                'deployments' => ($this->ops ?? throw new AgentException('Deployments unavailable'))->deployments(),
                'wordpress' => ($this->ops ?? throw new AgentException('WordPress unavailable'))->wordpress(),
                'database_overview' => ($this->services ?? throw new AgentException('Database status unavailable'))->databases(),
                'cache_overview' => ($this->services ?? throw new AgentException('Cache status unavailable'))->caches(),
                'web_stack' => ($this->services ?? throw new AgentException('Web stack status unavailable'))->webStack(),
                'ssl_overview' => ($this->security ?? throw new AgentException('Certificates unavailable'))->ssl(),
                default => throw new \InvalidArgumentException('No data for widget ' . $id),
            });
        } catch (AgentException) {
            return ['state' => 'unavailable', 'data' => [], 'at' => time()];
        } catch (\Throwable) {
            return ['state' => 'error', 'data' => [], 'at' => time()];
        }
        $state = ($r['data']['_state'] ?? 'ok');
        return ['state' => is_string($state) ? $state : 'ok', 'data' => $r['data'], 'at' => $r['at']];
    }

    /**
     * The Software card: every part of the server (components.status) with whether it is installed and running.
     *
     * @return array<string, mixed>
     */
    public function software(): array
    {
        if ($this->agent === null) {
            throw new AgentException('Agent unavailable');
        }
        $items = [];
        foreach ((array) json_decode((string) json_encode($this->agent->call('components.status', [], 60)), true) as $c) {
            $running = null;
            foreach ((array) ($c['steps'] ?? []) as $st) {
                if (($st['id'] ?? '') === 'running' && ($st['status'] ?? 'na') !== 'na') {
                    $running = $st['status'] === 'ok';
                }
            }
            $items[] = [
                'id' => (string) $c['id'], 'name' => (string) $c['name'], 'installed' => !empty($c['installed']),
                'running' => $running, 'unit' => ((array) ($c['units'] ?? []))[0] ?? null,
            ];
        }
        usort($items, static fn (array $a, array $b): int => [!$a['installed'], $a['name']] <=> [!$b['installed'], $b['name']]);
        return ['items' => $items, '_state' => $items === [] ? 'empty' : 'ok'];
    }

    /**
     * The Server card: what the agent reports about the machine.
     *
     * @return array<string, mixed>
     */
    public function serverInfo(): array
    {
        if ($this->agent === null) {
            throw new AgentException('Agent unavailable');
        }
        $info = (array) $this->agent->call('system.info');
        return ['info' => $info, '_state' => 'ok'];
    }

    /**
     * The Websites card: counts and PHP versions from the last discovery, and certificates close to expiry.
     *
     * @param array<string, mixed> $user
     * @return array<string, mixed>
     */
    public function websitesSummary(array $user): array
    {
        if ($this->discovery === null || $this->settings === null) {
            throw new AgentException('Discovery unavailable');
        }
        $inv = $this->discovery->inventory(false, (int) ($user['id'] ?? 0));
        $summary = DiscoveryService::summary($inv['data'], $this->settings->int('ssl_warning_days'));
        $summary['discovered_at'] = $inv['created_at'];
        return ['summary' => $summary, '_state' => 'ok'];
    }

    /**
     * Requests to every website, counted from their access logs: per hour for the last 24 hours against the 24 hours
     * before, the share of errors, and the busiest websites. A website whose log cannot be read is left out and counted
     * as such; with no readable log at all there is nothing to show (never a zero).
     *
     * @return array<string, mixed>
     */
    public function requestTrends(): array
    {
        if ($this->agent === null) {
            throw new AgentException('Agent unavailable');
        }
        $sites = $this->db->fetchAll('SELECT domain FROM websites WHERE server_id = ? AND enabled = 1 ORDER BY id LIMIT 30', [$this->server->id()]);
        $now = intdiv(time(), 3600) * 3600;
        $cut = $now - 23 * 3600;          // first hour of the last 24 hours
        $prevCut = $cut - 24 * 3600;      // first hour of the 24 hours before
        $hours = [];
        for ($t = $cut; $t <= $now; $t += 3600) {
            $hours[$t] = 0;
        }
        $total = $prev = $refused = 0;
        $classes = [];
        $perSite = [];
        $read = $skipped = 0;
        foreach ($sites as $s) {
            try {
                $r = (array) $this->agent->call('webreport.site', ['domain' => (string) $s['domain'], 'hours' => 48], 30);
            } catch (AgentException) {
                $skipped++;
                continue;
            }
            if (empty($r['exists'])) {
                $skipped++;
                continue;
            }
            $read++;
            $mine = 0;
            foreach ((array) ($r['timeline'] ?? []) as $b) {
                $b = (array) $b;
                $start = (int) ($b['start'] ?? 0);
                $n = (int) ($b['requests'] ?? 0);
                if (isset($hours[$start])) {
                    $hours[$start] += $n;
                    $mine += $n;
                } elseif ($start >= $prevCut && $start < $cut) {
                    $prev += $n;
                }
            }
            // The agent's status classes cover its whole 48-hour window; they are shown as shares of that window.
            foreach ((array) ($r['classes'] ?? []) as $k => $n) {
                if ((int) $k >= 2 && (int) $k <= 5) {
                    $classes[(int) $k] = ($classes[(int) $k] ?? 0) + (int) $n; // 2 = 2xx … 5 = 5xx
                }
            }
            $refused += (int) ($r['refused'] ?? 0);
            $total += $mine;
            if ($mine > 0) {
                $perSite[(string) $s['domain']] = $mine;
            }
        }
        if ($read === 0) {
            return ['_state' => $sites === [] ? 'empty' : 'unavailable', 'sites' => count($sites)];
        }
        arsort($perSite);
        $window = array_sum($classes);
        $errors = (int) (($classes[5] ?? 0) + ($classes[4] ?? 0));
        return [
            'hours' => $hours, 'total' => $total, 'previous' => $prev,
            'change' => $prev > 0 ? round(100 * ($total - $prev) / $prev, 1) : null,
            'error_share' => $window > 0 ? round(100 * $errors / $window, 1) : null,
            'server_errors' => (int) ($classes[5] ?? 0), 'refused' => $refused,
            'top' => array_slice($perSite, 0, 5, true), 'read' => $read, 'skipped' => $skipped,
            '_state' => 'ok',
        ];
    }

    /**
     * Websites that need attention (mail records, certificates, backups) and
     * certificates close to expiry.
     *
     * @param array<string, mixed> $user
     * @return array<string, mixed>
     */
    public function attention(array $user): array
    {
        $needs = $this->siteHealth?->overview($user);
        $expiring = null;
        if ($this->discovery !== null && $this->settings !== null) {
            try {
                $inv = $this->discovery->inventory(false, (int) ($user['id'] ?? 0));
                $expiring = (int) DiscoveryService::summary($inv['data'], $this->settings->int('ssl_warning_days'))['expiring'];
            } catch (AgentException) {
                $expiring = null; // certificate dates cannot be read now
            }
        }
        return ['needs' => $needs, 'expiring' => $expiring, '_state' => $needs === null && $expiring === null ? 'unavailable' : 'ok'];
    }

    /**
     * The numbers next to the performance charts: the latest reading of CPU,
     * memory, swap, load and network, and when the recorded history starts. The
     * history itself is drawn by the browser from /dashboard/series.
     *
     * @return array<string, mixed>
     */
    public function performanceHistory(): array
    {
        if ($this->agent === null) {
            throw new AgentException('Agent unavailable');
        }
        $m = (array) json_decode((string) json_encode($this->agent->call('system.metrics')), true);
        $mem = (array) ($m['memory'] ?? []);
        $rx = $tx = null;
        foreach ((array) ($m['network'] ?? []) as $n) {
            $n = (array) $n;
            $rx = ($rx ?? 0.0) + (float) ($n['rx_bytes_per_sec'] ?? 0);
            $tx = ($tx ?? 0.0) + (float) ($n['tx_bytes_per_sec'] ?? 0);
        }
        $swapTotal = (int) ($mem['swap_total_bytes'] ?? 0);
        return [
            'cpu' => isset($m['cpu_percent']) ? (float) $m['cpu_percent'] : null,
            'mem' => [
                'percent' => isset($mem['used_percent']) ? (float) $mem['used_percent'] : null,
                'used' => isset($mem['used_bytes']) ? (int) $mem['used_bytes'] : null,
                'available' => isset($mem['available_bytes']) ? (int) $mem['available_bytes'] : null,
                'total' => isset($mem['total_bytes']) ? (int) $mem['total_bytes'] : null,
                'swap_used' => $swapTotal > 0 ? (int) ($mem['swap_used_bytes'] ?? 0) : null,
                'swap_total' => $swapTotal > 0 ? $swapTotal : null,
            ],
            'load' => isset($m['load']) ? ['1' => (float) $m['load']['load1'], '5' => (float) $m['load']['load5'], '15' => (float) $m['load']['load15']] : null,
            'net' => $rx === null ? null : ['rx' => $rx, 'tx' => $tx],
            'since' => $this->monitoring?->firstSampleAt(),
        ];
    }

    /**
     * Websites by state, how many use HTTPS, and how many were created in each
     * of the last six months (websites that still exist; a deleted one is gone).
     *
     * @return array<string, mixed>
     */
    public function websiteOverview(): array
    {
        $sid = $this->server->id();
        $row = $this->db->fetch(
            'SELECT COUNT(*) AS total,
                    SUM(CASE WHEN ' . \NimVor\Repositories\UserRepository::SUSPENDED . ' THEN 1 ELSE 0 END) AS suspended,
                    SUM(CASE WHEN NOT ' . \NimVor\Repositories\UserRepository::SUSPENDED . ' AND w.enabled = 0 THEN 1 ELSE 0 END) AS disabled,
                    SUM(CASE WHEN NOT ' . \NimVor\Repositories\UserRepository::SUSPENDED . ' AND w.enabled = 1 THEN 1 ELSE 0 END) AS active,
                    SUM(CASE WHEN w.https = 1 THEN 1 ELSE 0 END) AS https
             FROM websites w LEFT JOIN users u ON u.id = w.owner_id WHERE w.server_id = ?',
            [$sid]
        ) ?? [];
        $total = (int) ($row['total'] ?? 0);
        $months = [];
        for ($i = 5; $i >= 0; $i--) {
            $months[gmdate('Y-m', strtotime("first day of -{$i} month"))] = 0;
        }
        foreach ($this->db->fetchAll("SELECT strftime('%Y-%m', created_at) AS m, COUNT(*) AS n FROM websites WHERE server_id = ? GROUP BY m", [$sid]) as $r) {
            if (isset($months[(string) $r['m']])) {
                $months[(string) $r['m']] = (int) $r['n'];
            }
        }
        return [
            'total' => $total,
            'active' => (int) ($row['active'] ?? 0),
            'disabled' => (int) ($row['disabled'] ?? 0),
            'suspended' => (int) ($row['suspended'] ?? 0),
            'https' => (int) ($row['https'] ?? 0),
            'created' => $months,
            '_state' => $total === 0 ? 'empty' : 'ok',
        ];
    }

    /**
     * The enabled services: how many run, are stopped or failed. The history
     * of how many ran is drawn from the recorded samples.
     *
     * @return array<string, mixed>
     */
    public function servicesOverview(): array
    {
        if ($this->agent === null) {
            throw new AgentException('Agent unavailable');
        }
        $list = (array) json_decode((string) json_encode($this->agent->call('services.status')), true);
        $seen = [];
        $up = $stopped = $failed = 0;
        $bad = [];
        foreach ($list as $svc) {
            $svc = (array) $svc;
            $unit = (string) ($svc['unit'] ?? '');
            if ($unit === '' || isset($seen[$unit]) || ($svc['unit_file_state'] ?? '') !== 'enabled') {
                continue;
            }
            $seen[$unit] = true;
            $state = (string) ($svc['active_state'] ?? '');
            if ($state === 'active') {
                $up++;
            } elseif ($state === 'failed') {
                $failed++;
                $bad[] = preg_replace('/\.service$/', '', $unit);
            } else {
                $stopped++;
                $bad[] = preg_replace('/\.service$/', '', $unit);
            }
        }
        return ['up' => $up, 'stopped' => $stopped, 'failed' => $failed, 'total' => $up + $stopped + $failed, 'problems' => array_slice($bad, 0, 4), '_state' => $up + $stopped + $failed === 0 ? 'empty' : 'ok'];
    }

    /**
     * Open alerts by severity (acknowledged ones counted apart), the ones to
     * look at first, and what was resolved in the last day.
     *
     * @return array<string, mixed>
     */
    public function alertCenter(): array
    {
        if ($this->monitoring === null) {
            throw new \InvalidArgumentException('Alerts unavailable');
        }
        $open = $this->monitoring->openAlerts();
        $counts = ['critical' => 0, 'warning' => 0, 'info' => 0];
        $acked = 0;
        foreach ($open as $a) {
            if (!empty($a['acknowledged_at'])) {
                $acked++;
                continue;
            }
            $sev = isset($counts[(string) $a['severity']]) ? (string) $a['severity'] : 'info';
            $counts[$sev]++;
        }
        $day = gmdate('c', time() - 86400);
        $recent = array_values(array_filter($this->monitoring->recentAlerts(20), static fn (array $a): bool => (string) $a['resolved_at'] >= $day));
        return [
            'counts' => $counts,
            'acknowledged' => $acked,
            'open' => array_map(static fn (array $a): array => [
                'id' => (int) $a['id'], 'severity' => (string) $a['severity'], 'message' => (string) $a['message'], 'first_seen' => (string) $a['first_seen'],
                'acknowledged' => !empty($a['acknowledged_at']),
            ], array_slice($open, 0, 10)),
            'open_total' => count($open),
            'resolved_24h' => count($recent),
            'recent' => array_map(static fn (array $a): array => ['message' => (string) $a['message'], 'resolved_at' => (string) $a['resolved_at']], array_slice($recent, 0, 3)),
        ];
    }

    /**
     * Website uptime and speed from the scheduler's HTTP checks. Never a zero
     * for something unmeasured: before the first check the widget is empty, and
     * when the agent cannot open connections it is unavailable.
     *
     * @return array<string, mixed>
     */
    public function websiteHealth(): array
    {
        $sum = $this->monitor?->summary();
        if ($this->monitor === null) {
            throw new AgentException('Website checks unavailable');
        }
        return $sum ?? [
            'none' => true,
            'enabled' => (int) $this->db->scalar('SELECT COUNT(*) FROM websites WHERE server_id = ? AND enabled = 1 AND monitor_disabled = 0', [$this->server->id()]),
            'unmonitored' => (int) $this->db->scalar('SELECT COUNT(*) FROM websites WHERE server_id = ? AND enabled = 1 AND monitor_disabled = 1', [$this->server->id()]),
        ];
    }

    /**
     * The five busiest processes by CPU and by memory (one half-second sample
     * by the agent). The Processes page has the full list and the confirmed
     * stop/kill actions.
     *
     * @return array<string, mixed>
     */
    public function topProcesses(): array
    {
        if ($this->agent === null) {
            throw new AgentException('Agent unavailable');
        }
        $live = (array) json_decode((string) json_encode($this->agent->call('system.live')), true);
        $pick = static fn (mixed $rows): array => array_map(static fn ($p): array => [
            'pid' => (int) ($p['pid'] ?? 0),
            'name' => (string) ($p['name'] ?? ''),
            'user' => (string) ($p['user'] ?? ''),
            'state' => (string) ($p['state'] ?? ''),
            'cpu' => isset($p['cpu_percent']) ? (float) $p['cpu_percent'] : null,
            'rss' => isset($p['rss_bytes']) ? (int) $p['rss_bytes'] : null,
            'mem' => isset($p['mem_percent']) ? (float) $p['mem_percent'] : null,
        ], array_slice(is_array($rows) ? $rows : [], 0, 5));
        $cpu = $pick($live['top_cpu'] ?? []);
        $mem = $pick($live['top_memory'] ?? []);
        return ['cpu' => $cpu, 'mem' => $mem, 'processes' => isset($live['processes']) ? (int) $live['processes'] : null, '_state' => $cpu === [] && $mem === [] ? 'empty' : 'ok'];
    }

    /**
     * Backups: the last completed one, failures in the last 7 days, runs now.
     *
     * @return array{last: ?string, failed: int, running: int, completed: int, schedules: int}
     */
    public function backups(): array
    {
        $sid = $this->server->id();
        $week = gmdate('c', time() - 7 * 86400);
        return [
            'last' => ($v = $this->db->scalar("SELECT MAX(created_at) FROM backups WHERE server_id = ? AND status = 'completed'", [$sid])) === null ? null : (string) $v,
            'failed' => (int) $this->db->scalar("SELECT COUNT(*) FROM backups WHERE server_id = ? AND status = 'failed' AND created_at >= ?", [$sid, $week]),
            'running' => (int) $this->db->scalar("SELECT COUNT(*) FROM backups WHERE server_id = ? AND status = 'running'", [$sid]),
            'completed' => (int) $this->db->scalar("SELECT COUNT(*) FROM backups WHERE server_id = ? AND status = 'completed'", [$sid]),
            'schedules' => (int) $this->db->scalar('SELECT COUNT(*) FROM backup_schedules WHERE server_id = ?', [$sid]),
        ];
    }

    /**
     * Disk: the filesystems now and the biggest places from the latest scan.
     *
     * @return array{filesystems: list<array<string, mixed>>, areas: list<array<string, mixed>>, scanned_at: ?string}
     */
    public function disk(): array
    {
        try {
            $fs = $this->disk->filesystems();
        } catch (AgentException) {
            $fs = [];
        }
        $scan = $this->disk->latest();
        $areas = array_values(array_filter((array) ($scan['result']['areas'] ?? []), static fn ($a): bool => empty(((array) $a)['missing'])));
        usort($areas, static fn ($a, $b): int => (int) ((array) $b)['bytes'] <=> (int) ((array) $a)['bytes']);
        return ['filesystems' => $fs, 'areas' => array_slice(array_map(static fn ($a): array => (array) $a, $areas), 0, 4), 'scanned_at' => $scan === null ? null : (string) ($scan['result']['scanned_at'] ?? $scan['finished_at'] ?? '')];
    }
}
