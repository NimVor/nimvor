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
        'subdomains' => ['max_subdomains', 'Subdomains'],
        'parked_domains' => ['max_parked_domains', 'Parked domains'],
        'addon_domains' => ['max_addon_domains', 'Addon domains'],
        'databases' => ['max_databases', 'SQL databases'],
        'ftp_accounts' => ['max_ftp_accounts', 'FTP accounts'],
        'email_accounts' => ['max_email_accounts', 'Email accounts'],
        'cron_jobs' => ['max_cron_jobs', 'Cron jobs'],
        'workers' => ['max_workers', 'Workers'],
        'customers' => ['max_customers', 'Customers'],
    ];

    /** Counted outside the panel's records (on the server); callers pass the count. */
    private const EXTERNAL = ['ftp_accounts'];

    public function __construct(private readonly Connection $db)
    {
    }

    /**
     * The plan in effect for a user: the assigned plan with the account's
     * own overrides applied (the plan itself is unchanged).
     *
     * @return array<string, mixed>|null
     */
    public function planFor(?int $userId): ?array
    {
        if ($userId === null) {
            return null;
        }
        $plan = $this->db->fetch('SELECT p.*, u.limit_overrides AS account_overrides FROM plans p JOIN users u ON u.plan_id = p.id WHERE u.id = ?', [$userId]);
        if ($plan === null) {
            return null;
        }
        foreach (self::decodeOverrides((string) $plan['account_overrides']) as $column => $value) {
            $plan[$column] = $value;
        }
        unset($plan['account_overrides']);
        return $plan;
    }

    /**
     * @return array<string, int> valid overrides (plan columns only)
     */
    public static function decodeOverrides(string $json): array
    {
        $v = json_decode($json, true);
        $out = [];
        foreach (is_array($v) ? $v : [] as $column => $value) {
            if (isset(PlanService::FIELDS[$column]) && is_int($value) && $value >= 0) {
                $out[$column] = $value;
            }
        }
        return $out;
    }

    /** @return array<string, int> */
    public function overrides(int $userId): array
    {
        return self::decodeOverrides((string) $this->db->scalar('SELECT limit_overrides FROM users WHERE id = ?', [$userId]));
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
            // Subdomains and addon domains have limits of their own.
            'websites' => "SELECT COUNT(*) FROM websites WHERE owner_id IN ({$in}) AND parent_id IS NULL",
            'databases' => "SELECT COUNT(*) FROM databases WHERE owner_id IN ({$in})",
            'cron_jobs' => "SELECT COUNT(*) FROM cron_jobs c JOIN websites w ON w.id = c.website_id WHERE w.owner_id IN ({$in})",
            'workers' => "SELECT COUNT(*) FROM workers k JOIN websites w ON w.id = k.website_id WHERE w.owner_id IN ({$in})",
            'customers' => "SELECT COUNT(*) FROM users WHERE parent_id IN ({$in})",
            'subdomains' => "SELECT COUNT(*) FROM websites c JOIN websites p ON p.id = c.parent_id WHERE c.owner_id IN ({$in}) AND c.domain LIKE '%.' || p.domain",
            'addon_domains' => "SELECT COUNT(*) FROM websites c JOIN websites p ON p.id = c.parent_id WHERE c.owner_id IN ({$in}) AND c.domain NOT LIKE '%.' || p.domain",
            'parked_domains' => "SELECT COUNT(*) FROM domains d JOIN websites w ON w.id = d.website_id WHERE d.is_primary = 0 AND w.owner_id IN ({$in})",
            'email_accounts' => "SELECT COUNT(*) FROM mail_mailboxes m JOIN mail_domains d ON d.id = m.domain_id WHERE d.owner_id IN ({$in})",
            'ftp_accounts' => '0',
            'bandwidth_mb' => "SELECT COALESCE(SUM(u.bw_bytes), 0) / 1048576 FROM website_usage u JOIN websites w ON w.id = u.website_id WHERE u.bw_month = '" . gmdate('Y-m') . "' AND w.owner_id IN ({$in})",
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

    /**
     * @param int $adding how many are being added (aliases come several at a time)
     * @param (callable(list<int>): int)|null $counter counts resources kept outside the panel's records
     */
    public function assertCanCreate(string $resource, ?int $ownerId, int $adding = 1, ?callable $counter = null): void
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
            $used = $counter !== null ? $counter($owners) : $this->count($resource, $owners);
            if ($used + $adding > $limit) {
                $whose = $planUser === $ownerId ? 'Your plan' : 'Your provider\'s plan';
                throw new UserFacingException(
                    "{$whose} allows at most {$limit} " . (ctype_upper(substr($label, 1, 1)) ? $label : lcfirst($label)) . '.',
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
    /**
     * @param array<string, int> $external counts of resources kept outside the panel's records
     */
    public function usage(int $userId, array $external = []): array
    {
        $plan = $this->planFor($userId);
        $owners = $this->family($userId);
        $out = [];
        $out[] = ['key' => 'disk', 'label' => 'Disk space (MB)', 'used' => $this->count('disk_mb', $owners), 'limit' => (int) ($plan['disk_quota_mb'] ?? 0)];
        $out[] = ['key' => 'bandwidth', 'label' => 'Bandwidth this month (MB)', 'used' => $this->count('bandwidth_mb', $owners), 'limit' => (int) ($plan['bandwidth_mb'] ?? 0)];
        foreach (self::RESOURCES as $res => [$column, $label]) {
            if (in_array($res, self::EXTERNAL, true) && !isset($external[$res])) {
                continue;
            }
            $out[] = ['key' => $res, 'label' => $label, 'used' => $external[$res] ?? $this->count($res, $owners), 'limit' => (int) ($plan[$column] ?? 0)];
        }
        return $out;
    }

    /** @return list<int> the user and their customers (public for counting outside) */
    public function owners(int $userId): array
    {
        return $this->family($userId);
    }

    /** Returns the plan's value for a numeric setting (0 = unlimited). */
    public function value(?int $ownerId, string $column): int
    {
        return (int) ($this->planFor($ownerId)[$column] ?? 0);
    }
}
