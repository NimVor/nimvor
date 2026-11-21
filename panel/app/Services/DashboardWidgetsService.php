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
                'attention' => $this->attention($user),
                'disk' => $this->disk(),
                'backups' => $this->backups() + ['attention' => (int) ($this->siteHealth?->overview($user)['counts']['backup'] ?? 0)],
                'top_processes' => $this->topProcesses(),
                'website_health' => $this->websiteHealth(),
                'security_overview' => ($this->security ?? throw new AgentException('Security status unavailable'))->security(),
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
        return $sum ?? ['none' => true, 'enabled' => (int) $this->db->scalar('SELECT COUNT(*) FROM websites WHERE server_id = ? AND enabled = 1', [$this->server->id()])];
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
