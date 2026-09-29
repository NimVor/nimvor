<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Database\Connection;
use WhaCP\Hosting\Scope;
use WhaCP\Repositories\UserRepository;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * Administration of hosting accounts: limit overrides on top of the plan,
 * plan upgrades/downgrades with the differences shown first, and monthly
 * bandwidth (accounts over their limit, resetting the month's counter).
 */
final class AccountAdminService
{
    /** Largest value an override may have (about 1 PB in MB, or a count). */
    public const MAX_VALUE = 1_000_000_000;

    public function __construct(
        private readonly Connection $db,
        private readonly UserRepository $users,
        private readonly PlanLimits $limits,
        private readonly PlanService $plans,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * A hosting account the actor may administer (administrators: any;
     * resellers: their customers, for plan changes only).
     *
     * @param array<string, mixed> $actor
     * @return array<string, mixed>
     */
    public function account(int $id, array $actor, bool $adminOnly): array
    {
        $target = $this->users->find($id);
        if ($target === null || !UserService::canManage($actor, $target) || ($target['role_scope'] ?? 'all') !== 'own') {
            throw new UserFacingException('The account no longer exists.', status: 404);
        }
        if ($adminOnly && !Scope::isAll($actor)) {
            throw new UserFacingException('Only administrators can do this.', status: 403);
        }
        return $target;
    }

    /**
     * Plan values, the account's overrides and the values in effect.
     *
     * @return list<array{column: string, label: string, plan: ?int, override: ?int, effective: ?int}>
     */
    public function limitsTable(array $account): array
    {
        $plan = $account['plan_id'] !== null ? $this->db->fetch('SELECT * FROM plans WHERE id = ?', [(int) $account['plan_id']]) : null;
        $over = $this->limits->overrides((int) $account['id']);
        $rows = [];
        foreach (PlanService::FIELDS as $column => $label) {
            $p = $plan !== null ? (int) $plan[$column] : null;
            $o = $over[$column] ?? null;
            $rows[] = ['column' => $column, 'label' => $label, 'plan' => $p, 'override' => $o, 'effective' => $plan === null ? null : ($o ?? $p)];
        }
        return $rows;
    }

    /**
     * Save overrides: a number sets one (0 = unlimited), an empty value
     * uses the plan's.
     *
     * @param array<string, mixed> $account
     * @param array<string, mixed> $input column => value
     * @param array<string, mixed> $actor
     */
    public function saveOverrides(array $account, array $input, array $actor): void
    {
        if ($account['plan_id'] === null) {
            throw new UserFacingException('Assign a hosting plan first: overrides change the limits of the account\'s plan.');
        }
        $before = $this->limits->overrides((int) $account['id']);
        $new = [];
        $errors = [];
        foreach (PlanService::FIELDS as $column => $label) {
            $raw = trim((string) ($input[$column] ?? ''));
            if ($raw === '') {
                continue;
            }
            $v = filter_var($raw, FILTER_VALIDATE_INT, ['options' => ['min_range' => 0, 'max_range' => self::MAX_VALUE]]);
            if ($v === false) {
                $errors[$column] = sprintf('%s: a whole number from 0 (unlimited) to %s, or empty for the plan\'s value.', $label, number_format(self::MAX_VALUE));
                continue;
            }
            $new[$column] = $v;
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        ksort($new);
        $this->db->run('UPDATE users SET limit_overrides = ?, updated_at = ? WHERE id = ?', [json_encode((object) $new, JSON_THROW_ON_ERROR), gmdate('c'), (int) $account['id']]);
        $this->audit->record('account.limits', 'user', (string) $account['id'], AuditLogger::SUCCESS, ['username' => $account['username'], 'before' => $before, 'after' => $new], (int) $actor['id']);
    }

    /**
     * Differences between the account's current limits and a target plan
     * (overrides stay and are shown separately), with current usage.
     *
     * @return list<array{label: string, from: int, to: int, used: ?int, override: ?int, over: bool}>
     */
    public function planDiff(array $account, array $target): array
    {
        $current = $account['plan_id'] !== null ? $this->db->fetch('SELECT * FROM plans WHERE id = ?', [(int) $account['plan_id']]) : null;
        $over = $this->limits->overrides((int) $account['id']);
        $usage = [];
        foreach ($this->limits->usage((int) $account['id']) as $u) {
            $usage[$u['key']] = (int) $u['used'];
        }
        $usageKey = ['disk_quota_mb' => 'disk', 'bandwidth_mb' => 'bandwidth'];
        foreach (PlanLimits::RESOURCES as $res => [$col]) {
            $usageKey[$col] = $res;
        }
        $rows = [];
        foreach (PlanService::FIELDS as $column => $label) {
            $from = (int) ($current[$column] ?? 0);
            $to = (int) $target[$column];
            $o = $over[$column] ?? null;
            $used = $usage[$usageKey[$column] ?? ''] ?? null;
            $effective = $o ?? $to;
            $rows[] = ['label' => $label, 'from' => $from, 'to' => $to, 'used' => $used, 'override' => $o, 'over' => $used !== null && $effective > 0 && $used > $effective];
        }
        return $rows;
    }

    /**
     * @param array<string, mixed> $account
     * @param array<string, mixed> $actor
     * @return array<string, mixed> the new plan
     */
    public function changePlan(array $account, int $planId, array $actor): array
    {
        $target = null;
        foreach ($this->plans->visible($actor) as $p) {
            if ((int) $p['id'] === $planId) {
                $target = $p;
            }
        }
        if ($target === null) {
            throw new UserFacingException('That plan does not exist or you cannot assign it.');
        }
        $from = $account['plan_id'] !== null ? (string) $this->db->scalar('SELECT name FROM plans WHERE id = ?', [(int) $account['plan_id']]) : '';
        $this->db->run('UPDATE users SET plan_id = ?, updated_at = ? WHERE id = ?', [$planId, gmdate('c'), (int) $account['id']]);
        $this->audit->record('account.plan_change', 'user', (string) $account['id'], AuditLogger::SUCCESS, ['username' => $account['username'], 'from' => $from, 'to' => $target['name']], (int) $actor['id']);
        return $target;
    }

    /**
     * Accounts with a monthly bandwidth limit, with this month's use.
     *
     * @return list<array{id: int, username: string, role: string, plan: string, used: int, limit: int, excess: int, suspended: bool, blocked: bool}>
     */
    public function bandwidth(bool $exceededOnly): array
    {
        $rows = [];
        $accounts = $this->db->fetchAll('SELECT u.id, u.username, r.name AS role, p.name AS plan, ' . UserRepository::SUSPENDED . ' AS suspended, ' . UserRepository::BANDWIDTH_BLOCKED . ' AS blocked FROM users u JOIN roles r ON r.id = u.role_id JOIN plans p ON p.id = u.plan_id ORDER BY u.username');
        foreach ($accounts as $a) {
            $limit = $this->limits->value((int) $a['id'], 'bandwidth_mb');
            if ($limit <= 0) {
                continue;
            }
            $used = $this->limits->count('bandwidth_mb', $this->limits->owners((int) $a['id']));
            if ($exceededOnly && $used <= $limit) {
                continue;
            }
            $rows[] = ['id' => (int) $a['id'], 'username' => (string) $a['username'], 'role' => (string) $a['role'], 'plan' => (string) $a['plan'], 'used' => $used, 'limit' => $limit, 'excess' => max(0, $used - $limit), 'suspended' => !empty($a['suspended']), 'blocked' => !empty($a['blocked'])];
        }
        usort($rows, static fn (array $x, array $y): int => $y['excess'] <=> $x['excess'] ?: $y['used'] <=> $x['used']);
        return $rows;
    }

    /**
     * Reset this month's bandwidth counter of the account's websites (and a
     * reseller's customers', which its limit includes). Log reading
     * positions and earlier months are untouched.
     *
     * @param array<string, mixed> $account
     * @param array<string, mixed> $actor
     * @return int MB that were counted
     */
    public function resetBandwidth(array $account, array $actor): int
    {
        $owners = $this->limits->owners((int) $account['id']);
        $used = $this->limits->count('bandwidth_mb', $owners);
        $in = implode(',', array_map('intval', $owners));
        $this->db->run("UPDATE website_usage SET bw_bytes = 0 WHERE bw_month = ? AND website_id IN (SELECT id FROM websites WHERE owner_id IN ({$in}))", [gmdate('Y-m')]);
        $this->audit->record('account.bandwidth_reset', 'user', (string) $account['id'], AuditLogger::SUCCESS, ['username' => $account['username'], 'month' => gmdate('Y-m'), 'mb_before' => $used], (int) $actor['id']);
        return $used;
    }
}
