<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Database\Connection;
use WhaCP\Support\UserFacingException;

/**
 * Enforces hosting-plan limits for owned resources.
 *
 * A customer's own plan limits what they own. When the customer belongs to
 * a reseller, the reseller's plan also limits the total of the reseller and
 * all their customers together. Users without a plan are unlimited; a
 * limit of 0 means unlimited.
 */
final class PlanLimits
{
    public const RESOURCES = [
        'websites' => ['max_websites', 'Websites'],
        'databases' => ['max_databases', 'Databases'],
        'cron_jobs' => ['max_cron_jobs', 'Cron jobs'],
        'workers' => ['max_workers', 'Workers'],
        'customers' => ['max_customers', 'Customers'],
    ];

    public function __construct(private readonly Connection $db)
    {
    }

    /** @return array<string, mixed>|null */
    public function planFor(?int $userId): ?array
    {
        if ($userId === null) {
            return null;
        }
        return $this->db->fetch('SELECT p.* FROM plans p JOIN users u ON u.plan_id = p.id WHERE u.id = ?', [$userId]);
    }

    /**
     * Number of resources owned by the given users.
     *
     * @param list<int> $owners
     */
    public function count(string $resource, array $owners): int
    {
        if ($owners === []) {
            return 0;
        }
        $in = implode(',', array_map('intval', $owners));
        $sql = match ($resource) {
            'websites' => "SELECT COUNT(*) FROM websites WHERE owner_id IN ({$in})",
            'databases' => "SELECT COUNT(*) FROM databases WHERE owner_id IN ({$in})",
            'cron_jobs' => "SELECT COUNT(*) FROM cron_jobs c JOIN websites w ON w.id = c.website_id WHERE w.owner_id IN ({$in})",
            'workers' => "SELECT COUNT(*) FROM workers k JOIN websites w ON w.id = k.website_id WHERE w.owner_id IN ({$in})",
            'customers' => "SELECT COUNT(*) FROM users WHERE parent_id IN ({$in})",
            'disk_mb' => "SELECT COALESCE(SUM(u.bytes), 0) / 1048576 FROM website_usage u JOIN websites w ON w.id = u.website_id WHERE w.owner_id IN ({$in})",
            default => throw new \InvalidArgumentException($resource),
        };
        return (int) $this->db->scalar($sql);
    }

    /** @return list<int> the user and their customers */
    private function family(int $userId): array
    {
        $ids = array_map('intval', array_column($this->db->fetchAll('SELECT id FROM users WHERE parent_id = ?', [$userId]), 'id'));
        return array_merge([$userId], $ids);
    }

    public function assertCanCreate(string $resource, ?int $ownerId): void
    {
        if ($ownerId === null || !isset(self::RESOURCES[$resource])) {
            return;
        }
        [$column, $label] = self::RESOURCES[$resource];
        // The owner's own plan: resellers count their customers' resources too.
        $checks = [[$ownerId, $this->family($ownerId)]];
        $parent = $this->db->scalar('SELECT parent_id FROM users WHERE id = ?', [$ownerId]);
        if ($parent !== null && $resource !== 'customers') {
            $checks[] = [(int) $parent, $this->family((int) $parent)];
        }
        foreach ($checks as [$planUser, $owners]) {
            $plan = $this->planFor($planUser);
            $limit = (int) ($plan[$column] ?? 0);
            if ($plan === null || $limit <= 0) {
                continue;
            }
            if ($this->count($resource, $owners) >= $limit) {
                $whose = $planUser === $ownerId ? 'Your plan' : 'Your provider\'s plan';
                throw new UserFacingException(
                    "{$whose} allows at most {$limit} " . strtolower($label) . '.',
                    'The limit of the “' . $plan['name'] . '” plan has been reached.',
                    'Delete an existing one or ask your provider to upgrade the plan.',
                );
            }
        }
    }

    /**
     * Usage against the plan of a user (for display).
     *
     * @return list<array{label: string, used: int, limit: int}>
     */
    public function usage(int $userId): array
    {
        $plan = $this->planFor($userId);
        $owners = $this->family($userId);
        $out = [];
        foreach (self::RESOURCES as $res => [$column, $label]) {
            $out[] = ['label' => $label, 'used' => $this->count($res, $owners), 'limit' => (int) ($plan[$column] ?? 0)];
        }
        $out[] = ['label' => 'Disk (MB)', 'used' => $this->count('disk_mb', $owners), 'limit' => (int) ($plan['disk_quota_mb'] ?? 0)];
        return $out;
    }

    /** Returns the plan's value for a numeric setting (0 = unlimited). */
    public function value(?int $ownerId, string $column): int
    {
        return (int) ($this->planFor($ownerId)[$column] ?? 0);
    }
}
