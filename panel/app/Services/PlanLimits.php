<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Database\Connection;
use WhaCP\Support\UserFacingException;

/**
 * Enforces hosting-plan limits for owned resources.
 *
 * Users without a plan (administrators, operators) are unlimited.
 */
final class PlanLimits
{
    /** resource => [plan column, count query] */
    private const RESOURCES = [
        'websites' => ['max_websites', 'SELECT COUNT(*) FROM websites WHERE owner_id = ?'],
        'databases' => ['max_databases', 'SELECT COUNT(*) FROM databases WHERE owner_id = ?'],
        'cron_jobs' => ['max_cron_jobs', 'SELECT COUNT(*) FROM cron_jobs c JOIN websites w ON w.id = c.website_id WHERE w.owner_id = ?'],
        'workers' => ['max_workers', 'SELECT COUNT(*) FROM workers k JOIN websites w ON w.id = k.website_id WHERE w.owner_id = ?'],
        'customers' => ['max_customers', 'SELECT COUNT(*) FROM users WHERE parent_id = ?'],
    ];

    public function __construct(private readonly Connection $db)
    {
    }

    /** @return array<string, mixed>|null */
    public function planFor(?int $userId): ?array
    {
        if ($userId === null || !$this->tableExists('plans')) {
            return null;
        }
        return $this->db->fetch('SELECT p.* FROM plans p JOIN users u ON u.plan_id = p.id WHERE u.id = ?', [$userId]);
    }

    public function assertCanCreate(string $resource, ?int $ownerId): void
    {
        $plan = $this->planFor($ownerId);
        if ($plan === null || !isset(self::RESOURCES[$resource])) {
            return;
        }
        [$column, $sql] = self::RESOURCES[$resource];
        $limit = (int) ($plan[$column] ?? 0);
        if ($limit <= 0) {
            return; // 0 = unlimited
        }
        if ((int) $this->db->scalar($sql, [$ownerId]) >= $limit) {
            throw new UserFacingException(
                "Your plan allows at most {$limit} " . str_replace('_', ' ', $resource) . '.',
                'The limit of the “' . $plan['name'] . '” plan has been reached.',
                'Delete an existing one or ask your provider to upgrade the plan.',
            );
        }
    }

    /** Returns the plan's value for a numeric setting (0 = unlimited). */
    public function value(?int $ownerId, string $column): int
    {
        return (int) ($this->planFor($ownerId)[$column] ?? 0);
    }

    private function tableExists(string $t): bool
    {
        return (bool) $this->db->scalar("SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?", [$t]);
    }
}
