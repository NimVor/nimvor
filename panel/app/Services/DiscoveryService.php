<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Repositories\DiscoverySnapshotRepository;

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
        private readonly \WhaCP\Repositories\SettingRepository $state,
    ) {
    }

    /** Force the next inventory() call to ask the agent (after a change). */
    public function invalidate(): void
    {
        $this->state->set('_discovery_invalidated_at', gmdate('c'), null);
    }

    /**
     * Latest inventory, refreshed from the agent when older than the
     * configured cache time (or when $refresh is true).
     *
     * @return array{data: array<string, mixed>, created_at: string, from_cache: bool}
     */
    public function inventory(bool $refresh = false, ?int $userId = null): array
    {
        $latest = $this->snapshots->latest();
        $ttl = $this->settings->int('discovery_cache_seconds');
        $invalidated = (string) ($this->state->all()['_discovery_invalidated_at'] ?? '');
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
        $this->snapshots->store($data, $count, $userId);
        if ($manual) {
            $this->audit->record('discovery.run', 'server', 'local', AuditLogger::SUCCESS, ['websites' => $count]);
        }
        return ['data' => $data, 'created_at' => gmdate('c'), 'from_cache' => false];
    }

    /**
     * @param array<string, mixed> $inventory
     * @return list<array<string, mixed>>
     */
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
