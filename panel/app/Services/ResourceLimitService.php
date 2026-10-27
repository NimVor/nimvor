<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Repositories\WebsiteRepository;

/**
 * Resource limits (CPU, memory, processes, disk speed) of hosting accounts,
 * from their plans, applied by the agent with cgroups: one slice per
 * account (acct<user id>) holding its websites' PHP, cron jobs and workers.
 *
 * reconcile() compares what the plans ask for with what the agent applied,
 * changes only the accounts that differ and then re-applies their websites,
 * cron jobs and workers (so PHP-FPM and running workers move in or out of
 * the slice). It runs after plan and account changes and from the scheduler.
 */
final class ResourceLimitService
{
    public const FIELDS = ['cpu_percent', 'memory_mb', 'tasks_max', 'io_read_mbps', 'io_write_mbps'];

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
        private readonly WebsiteRepository $sites,
        private readonly WebsiteService $websites,
        private readonly CronService $cron,
        private readonly WorkerService $workers,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * Effective limits of a user: its plan's values, bounded by its
     * reseller's plan (a reseller's customers get no more than it has).
     *
     * @return array<string, int>
     */
    public function forUser(int $userId): array
    {
        $row = $this->db->fetch(
            'SELECT ro.scope, ' . implode(', ', array_map(static fn (string $f): string => 'COALESCE(p.' . $f . ', 0) AS ' . $f . ', COALESCE(rp.' . $f . ', 0) AS r_' . $f, self::FIELDS)) . '
             FROM users u JOIN roles ro ON ro.id = u.role_id LEFT JOIN plans p ON p.id = u.plan_id
             LEFT JOIN users r ON r.id = u.parent_id LEFT JOIN plans rp ON rp.id = r.plan_id WHERE u.id = ?',
            [$userId]
        );
        $out = array_fill_keys(self::FIELDS, 0);
        if ($row === null || $row['scope'] === 'all') {
            return $out; // administrators' websites are not limited
        }
        foreach (self::FIELDS as $f) {
            $own = (int) $row[$f];
            $cap = (int) $row['r_' . $f];
            $out[$f] = $cap > 0 && ($own === 0 || $own > $cap) ? $cap : $own;
        }
        return $out;
    }

    /**
     * What every account should have: account => limits and system users.
     * A system user shared by websites of several accounts belongs to the
     * account of its oldest website.
     *
     * @return array<string, array{owner_id: int, limits: array<string, int>, users: list<string>}>
     */
    public function desired(): array
    {
        $rows = $this->db->fetchAll(
            "SELECT w.system_user, MIN(w.id) AS first_id FROM websites w WHERE w.server_id = ? AND w.system_user <> '' GROUP BY w.system_user",
            [$this->server->id()]
        );
        $out = [];
        foreach ($rows as $r) {
            $owner = (int) $this->db->scalar('SELECT owner_id FROM websites WHERE id = ?', [$r['first_id']]);
            if ($owner <= 0) {
                continue;
            }
            $limits = $this->forUser($owner);
            if (array_sum($limits) === 0) {
                continue;
            }
            $key = 'acct' . $owner;
            $out[$key] ??= ['owner_id' => $owner, 'limits' => $limits, 'users' => []];
            $out[$key]['users'][] = (string) $r['system_user'];
        }
        foreach ($out as &$a) {
            sort($a['users']);
        }
        return $out;
    }

    /** @return array<string, mixed> the agent's applied limits and usage */
    public function status(): array
    {
        return (array) json_decode((string) json_encode($this->agent->call('limits.status', [], 30)), true);
    }

    /** @return list<string> what changed */
    public function reconcile(): array
    {
        $applied = (array) ($this->status()['accounts'] ?? []);
        $want = $this->desired();
        $lines = [];
        foreach (array_unique(array_merge(array_keys($applied), array_keys($want))) as $acct) {
            $w = $want[$acct] ?? null;
            $cur = $applied[$acct] ?? null;
            $wantLimits = $w['limits'] ?? array_fill_keys(self::FIELDS, 0);
            $wantUsers = $w['users'] ?? [];
            $curUsers = (array) ($cur['users'] ?? []);
            sort($curUsers);
            $same = $cur !== null && $w !== null && $curUsers === $wantUsers
                && array_intersect_key((array) $cur['limits'], $wantLimits) == $wantLimits;
            if ($same || ($cur === null && $w === null)) {
                continue;
            }
            // Users that leave the account are sent too, so their logins lose its limits.
            $users = array_values(array_unique(array_merge($wantUsers, $curUsers)));
            if ($w === null) {
                $users = $curUsers;
            }
            $this->agent->call('limits.set', ['account' => $acct, 'users' => $w === null ? $users : $wantUsers] + $wantLimits, 60);
            $this->audit->record('limits.apply', 'server', 'limits', AuditLogger::SUCCESS, ['account' => $acct, 'users' => $wantUsers, 'limits' => $wantLimits]);
            $lines[] = $acct . ': ' . ($w === null ? 'limits removed' : 'limits applied (' . implode(', ', array_map(static fn (string $k, int $v): string => $k . '=' . $v, array_keys($wantLimits), $wantLimits)) . ')');
            foreach ($users as $u) {
                $this->reapplyUser($u, $lines);
            }
        }
        return $lines;
    }

    /**
     * Re-apply a system user's websites, cron jobs and workers, so they run
     * in (or leave) the account's slice.
     *
     * @param list<string> $lines
     */
    private function reapplyUser(string $user, array &$lines): void
    {
        foreach ($this->db->fetchAll('SELECT id FROM websites WHERE server_id = ? AND system_user = ?', [$this->server->id(), $user]) as $row) {
            $site = $this->sites->find($this->server->id(), (int) $row['id'], null);
            if ($site === null) {
                continue;
            }
            try {
                $this->websites->reapply($site);
                $this->cron->apply($site);
                foreach ($this->workers->forSite($site) as $wk) {
                    $this->workers->apply($site, $wk);
                }
            } catch (\Throwable $e) {
                $lines[] = $site['domain'] . ': not re-applied: ' . $e->getMessage();
            }
        }
    }
}
