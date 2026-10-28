<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Database\Connection;

/**
 * Enforces the plans' disk space with Linux user quotas (agent quota.*).
 *
 * An administrator turns quotas on once per server (enable()). Then every
 * scheduler pass, and after plan, limit and website changes, reconcile()
 * reads each website system user's use and sets its hard limit:
 *
 * - an account's limit (its plan's disk space, with its overrides) is
 *   shared by its system users: each may grow by an equal part of what is
 *   left, re-divided every pass, so together they never pass the limit;
 * - a reseller's limit covers its customers' users as well; a user gets the
 *   smaller of the two parts;
 * - administrators and accounts without a limit are not limited.
 *
 * Writing beyond the limit fails with "Disk quota exceeded". Files owned by
 * the account's system users on the websites' file system count; email
 * (stored as the mail system's user) and databases do not.
 */
final class DiskQuotaService
{
    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
        private readonly PlanLimits $limits,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @return array<string, mixed> the agent's quota.status */
    public function status(): array
    {
        return (array) json_decode((string) json_encode($this->agent->call('quota.status', [], 60)), true);
    }

    /**
     * Start turning quotas on (a background job).
     *
     * @param array<string, mixed> $actor
     */
    public function enable(array $actor): string
    {
        $res = (array) $this->agent->call('quota.enable', [], 60);
        $job = (string) ($res['job_id'] ?? '');
        $this->audit->record('quota.enable', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['job_id' => $job], (int) $actor['id']);
        return $job;
    }

    /** @return list<array<string, mixed>> system users with use and limit (last read) */
    public function rows(): array
    {
        return $this->db->fetchAll('SELECT q.*, (SELECT u.username FROM websites w JOIN users u ON u.id = w.owner_id WHERE w.server_id = q.server_id AND w.system_user = q.system_user ORDER BY w.id LIMIT 1) AS account FROM disk_quotas q WHERE q.server_id = ? ORDER BY q.system_user', [$this->server->id()]);
    }

    /**
     * Hard limits (KiB, 0 = none) for each system user.
     *
     * @param array<string, int|null> $owners system user => owning account
     * @param array<string, int> $used system user => KiB used
     * @param array<int, int> $limitKb account => its limit in KiB (limited accounts only)
     * @param array<int, int|null> $parents account => its reseller
     * @return array<string, int>
     */
    public static function split(array $owners, array $used, array $limitKb, array $parents): array
    {
        // Each limited account's members: its users and its customers' users.
        $members = [];
        foreach ($owners as $user => $owner) {
            if ($owner === null) {
                continue;
            }
            foreach ([$owner, $parents[$owner] ?? null] as $holder) {
                if ($holder !== null && isset($limitKb[$holder])) {
                    $members[$holder][] = $user;
                }
            }
        }
        $share = [];
        foreach ($members as $holder => $users) {
            $left = $limitKb[$holder] - array_sum(array_map(static fn (string $u): int => $used[$u] ?? 0, $users));
            $share[$holder] = intdiv(max(0, $left), count($users));
        }
        $out = [];
        foreach ($owners as $user => $owner) {
            $parts = [];
            foreach ([$owner, $owner === null ? null : ($parents[$owner] ?? null)] as $holder) {
                if ($holder !== null && isset($share[$holder])) {
                    $parts[] = $share[$holder];
                }
            }
            // 0 would mean "no limit": a full account still gets its use as the limit.
            $out[$user] = $parts === [] ? 0 : max(1, ($used[$user] ?? 0) + min($parts));
        }
        return $out;
    }

    /** @return list<string> what changed */
    public function reconcile(): array
    {
        if (($this->status()['state'] ?? '') !== 'on') {
            return [];
        }
        $sid = $this->server->id();
        // A system user belongs to the account of its oldest website.
        $owners = [];
        foreach ($this->db->fetchAll("SELECT w.system_user, (SELECT w2.owner_id FROM websites w2 WHERE w2.server_id = w.server_id AND w2.system_user = w.system_user ORDER BY w2.id LIMIT 1) AS owner_id FROM websites w WHERE w.server_id = ? AND w.system_user <> '' GROUP BY w.system_user", [$sid]) as $r) {
            $owners[(string) $r['system_user']] = $r['owner_id'] === null ? null : (int) $r['owner_id'];
        }
        $known = array_column($this->db->fetchAll('SELECT system_user, hard_kb FROM disk_quotas WHERE server_id = ?', [$sid]), 'hard_kb', 'system_user');
        $users = array_values(array_unique(array_merge(array_keys($owners), array_map('strval', array_keys($known)))));
        if ($users === []) {
            return [];
        }
        $report = (array) (((array) $this->agent->call('quota.report', ['users' => $users], 120))['users'] ?? []);
        $used = $current = [];
        foreach ($report as $u => $q) {
            $q = (array) $q;
            $used[(string) $u] = (int) ($q['used_kb'] ?? 0);
            $current[(string) $u] = (int) ($q['hard_kb'] ?? 0);
        }
        // Limits in effect: plan and overrides; administrators are not limited.
        $limitKb = $parents = [];
        $accounts = array_unique(array_filter(array_values($owners), static fn ($o): bool => $o !== null));
        foreach ($accounts as $id) {
            $row = $this->db->fetch('SELECT u.parent_id, r.scope FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = ?', [$id]);
            if ($row === null) {
                continue;
            }
            $parents[$id] = $row['parent_id'] === null ? null : (int) $row['parent_id'];
            foreach (array_filter([$id, $parents[$id]]) as $holder) {
                if (!isset($limitKb[$holder])) {
                    $scope = $holder === $id ? $row['scope'] : $this->db->scalar('SELECT r.scope FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = ?', [$holder]);
                    $mb = $scope === 'all' ? 0 : $this->limits->value($holder, 'disk_quota_mb');
                    if ($mb > 0) {
                        $limitKb[$holder] = $mb * 1024;
                    }
                }
            }
        }
        $want = self::split($owners, $used, $limitKb, $parents);
        // Users without websites any more lose their limit.
        foreach (array_keys($known) as $u) {
            $want[(string) $u] ??= 0;
        }
        $changes = [];
        foreach ($want as $u => $kb) {
            if (!array_key_exists($u, $current)) {
                continue; // no such system user (yet)
            }
            $cur = $current[$u];
            $differs = $kb === 0 || $cur === 0 ? $kb !== $cur : abs($kb - $cur) > max(1024, intdiv($kb, 100));
            if ($differs) {
                $changes[$u] = $kb;
            }
        }
        $lines = [];
        $failed = [];
        if ($changes !== []) {
            $res = (array) $this->agent->call('quota.set', ['limits' => $changes], 120);
            $failed = (array) ($res['failed'] ?? []);
            foreach ($changes as $u => $kb) {
                if (isset($failed[$u])) {
                    $lines[] = $u . ': disk limit not set: ' . $failed[$u];
                    continue;
                }
                $current[$u] = $kb;
                $lines[] = $u . ': disk limit ' . ($kb === 0 ? 'removed' : 'set to ' . round($kb / 1024) . ' MB');
            }
            $this->audit->record('quota.apply', 'server', 'quota', $failed === [] ? AuditLogger::SUCCESS : AuditLogger::FAILURE, ['limits_kb' => $changes, 'failed' => array_keys($failed)]);
        }
        $now = gmdate('c');
        $this->db->transaction(function () use ($sid, $owners, $used, $current, $known, $now): void {
            foreach (array_keys($known) as $u) {
                if (!array_key_exists((string) $u, $current)) {
                    // The system user no longer exists.
                    $this->db->run('DELETE FROM disk_quotas WHERE server_id = ? AND system_user = ?', [$sid, (string) $u]);
                }
            }
            foreach ($current as $u => $hard) {
                if (!isset($owners[$u]) && $hard === 0) {
                    $this->db->run('DELETE FROM disk_quotas WHERE server_id = ? AND system_user = ?', [$sid, $u]);
                    continue;
                }
                $this->db->run('INSERT INTO disk_quotas (server_id, system_user, used_kb, hard_kb, measured_at) VALUES (?, ?, ?, ?, ?) ON CONFLICT (server_id, system_user) DO UPDATE SET used_kb = excluded.used_kb, hard_kb = excluded.hard_kb, measured_at = excluded.measured_at', [$sid, $u, $used[$u] ?? 0, $hard, $now]);
            }
        });
        return $lines;
    }
}
