<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Repositories\DiscoverySnapshotRepository;

/**
 * Read-only server inventory.
 *
 * The agent performs discovery; the panel stores the result as a snapshot so
 * pages load quickly and so there is a history of what was observed. Nothing
 * here modifies the server.
 */
final class DiscoveryService
{
    public function __construct(
        private readonly AgentClient $agent,
        private readonly DiscoverySnapshotRepository $snapshots,
        private readonly SettingsService $settings,
        private readonly AuditLogger $audit,
        private readonly \NimVor\Repositories\SettingRepository $state,
        private readonly ?ServerContext $server = null,
    ) {
    }

    private function serverId(): int
    {
        return $this->server?->id() ?? 1;
    }

    /** Force the next inventory() call to ask the agent (after a change). */
    public function invalidate(): void
    {
        $this->state->set('_discovery_invalidated_at:' . $this->serverId(), gmdate('c'), null);
    }

    /**
     * Latest inventory, refreshed from the agent when older than the
     * configured cache time (or when $refresh is true).
     *
     * @return array{data: array<string, mixed>, created_at: string, from_cache: bool}
     */
    public function inventory(bool $refresh = false, ?int $userId = null): array
    {
        $latest = $this->snapshots->latest($this->serverId());
        $ttl = $this->settings->int('discovery_cache_seconds');
        $invalidated = (string) ($this->state->all()['_discovery_invalidated_at:' . $this->serverId()] ?? '');
        $fresh = $latest !== null && strtotime($latest['created_at']) >= time() - $ttl
            && ($invalidated === '' || strtotime($latest['created_at']) > strtotime($invalidated));
        if (!$refresh && $fresh) {
            return ['data' => $latest['data'], 'created_at' => $latest['created_at'], 'from_cache' => true];
        }
        return $this->run($userId, $refresh);
    }

    /**
     * Ask the agent for a fresh inventory and store it.
     *
     * @return array{data: array<string, mixed>, created_at: string, from_cache: bool}
     */
    public function run(?int $userId, bool $manual = true): array
    {
        $data = $this->agent->call('discovery.inventory');
        $data = is_array($data) ? $data : [];
        $count = count($data['websites']['websites'] ?? []);
        $this->snapshots->store($data, $count, $userId, 20, $this->serverId());
        if ($manual) {
            $this->audit->record('discovery.run', 'server', (string) $this->serverId(), AuditLogger::SUCCESS, ['websites' => $count]);
        }
        return ['data' => $data, 'created_at' => gmdate('c'), 'from_cache' => false];
    }

    /**
     * @param array<string, mixed> $inventory
     * @return list<array<string, mixed>>
     */
    /**
     * The part of the server inventory a user may see. Roles with server-wide
     * scope see everything. Others (resellers, customers) see only the
     * websites they own (by domain) and those websites' certificates and PHP
     * pools. They never see system cron jobs, accounts, services or database
     * schemas, which may belong to anyone on the server.
     *
     * @param array<string, mixed> $inventory
     * @param list<string> $ownDomains
     * @return array<string, mixed>
     */
    public static function forScope(array $inventory, bool $all, array $ownDomains): array
    {
        if ($all) {
            return $inventory;
        }
        $own = array_fill_keys(array_map('strtolower', $ownDomains), true);
        $mine = static function (array $names) use ($own): bool {
            foreach ($names as $n) {
                if (isset($own[strtolower((string) $n)])) {
                    return true;
                }
            }
            return false;
        };
        $sites = array_values(array_filter(self::websites($inventory), static fn (array $w): bool => isset($own[strtolower((string) ($w['domain'] ?? ''))])));
        $pools = [];
        foreach ($sites as $w) {
            if (isset($w['php']['pool'])) {
                $pools[(string) $w['php']['pool']] = true;
            }
        }
        $out = $inventory;
        $out['websites'] = ['websites' => $sites, 'catch_all_vhosts' => 0, 'warnings' => []];
        $out['certificates'] = array_values(array_filter((array) ($inventory['certificates'] ?? []), static fn ($c): bool => is_array($c) && $mine(array_merge([(string) ($c['name'] ?? '')], (array) ($c['domains'] ?? []), (array) ($c['certificate']['names'] ?? [])))));
        $php = (array) ($inventory['php'] ?? []);
        $php['pools'] = array_values(array_filter((array) ($php['pools'] ?? []), static fn ($p): bool => is_array($p) && isset($pools[(string) ($p['name'] ?? '')])));
        $php['warnings'] = [];
        $out['php'] = $php;
        $out['cron'] = ['jobs' => [], 'warnings' => []];
        $out['databases'] = ['installed' => (bool) ($inventory['databases']['installed'] ?? false), 'schemas' => [], 'warnings' => []];
        $out['users'] = [];
        $out['services'] = [];
        return $out;
    }

    public static function websites(array $inventory): array
    {
        $list = $inventory['websites']['websites'] ?? [];
        return is_array($list) ? array_values($list) : [];
    }

    /**
     * @param array<string, mixed> $inventory
     * @return array<string, mixed>|null
     */
    public static function website(array $inventory, string $id): ?array
    {
        foreach (self::websites($inventory) as $site) {
            if (($site['id'] ?? null) === $id) {
                return $site;
            }
        }
        return null;
    }

    /**
     * Aggregate figures for the dashboard.
     *
     * @param array<string, mixed> $inventory
     * @return array{total: int, enabled: int, https: int, php_versions: array<string, int>, expiring: int}
     */
    public static function summary(array $inventory, int $warnDays = 14): array
    {
        $sites = self::websites($inventory);
        $php = [];
        $enabled = $https = $expiring = 0;
        foreach ($sites as $s) {
            if (!empty($s['enabled'])) {
                $enabled++;
            }
            if (!empty($s['https'])) {
                $https++;
            }
            if (isset($s['php']['version']) && $s['php']['version'] !== '') {
                $v = (string) $s['php']['version'];
                $php[$v] = ($php[$v] ?? 0) + 1;
            }
            if (isset($s['ssl']['days_remaining']) && (int) $s['ssl']['days_remaining'] <= $warnDays) {
                $expiring++;
            }
        }
        ksort($php, SORT_NATURAL);
        return ['total' => count($sites), 'enabled' => $enabled, 'https' => $https, 'php_versions' => $php, 'expiring' => $expiring];
    }
}
