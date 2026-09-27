<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Database\Connection;
use WhaCP\Hosting\Scope;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * Hosting plans. Administrators manage global plans; resellers manage
 * their own plans and can only assign those to their customers.
 */
final class PlanService
{
    public const FIELDS = [
        'disk_quota_mb' => 'Disk space quota (MB)',
        'bandwidth_mb' => 'Monthly bandwidth (MB)',
        'max_websites' => 'Websites',
        'max_subdomains' => 'Subdomains',
        'max_parked_domains' => 'Parked domains (aliases)',
        'max_addon_domains' => 'Addon domains',
        'max_databases' => 'SQL databases',
        'max_ftp_accounts' => 'FTP accounts',
        'max_email_accounts' => 'Email accounts',
        'max_email_quota_mb' => 'Max quota per email address (MB)',
        'max_cron_jobs' => 'Cron jobs',
        'max_workers' => 'Workers (applications)',
        'max_customers' => 'Customers (resellers)',
    ];

    public function __construct(
        private readonly Connection $db,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * Plans the actor may see and assign.
     *
     * @param array<string, mixed> $actor
     * @return list<array<string, mixed>>
     */
    public function visible(array $actor): array
    {
        $sql = 'SELECT p.*, (SELECT COUNT(*) FROM users u WHERE u.plan_id = p.id) AS users, o.username AS owner_name FROM plans p LEFT JOIN users o ON o.id = p.owner_id';
        return Scope::isAll($actor)
            ? $this->db->fetchAll($sql . ' ORDER BY p.owner_id IS NOT NULL, p.name')
            : $this->db->fetchAll($sql . ' WHERE p.owner_id = ? ORDER BY p.name', [(int) $actor['id']]);
    }

    /**
     * @param array<string, mixed> $actor
     * @return array<string, mixed>|null
     */
    public function find(array $actor, int $id): ?array
    {
        foreach ($this->visible($actor) as $p) {
            if ((int) $p['id'] === $id) {
                return $p;
            }
        }
        return null;
    }

    /** @param array<string, mixed> $actor */
    public function assertAssignable(array $actor, ?int $planId): void
    {
        if ($planId !== null && $this->find($actor, $planId) === null) {
            throw new ValidationException(['plan_id' => 'Choose one of your plans.']);
        }
    }

    /**
     * @param array<string, mixed> $actor
     * @param array<string, mixed> $input
     */
    public function save(array $actor, ?int $id, array $input): int
    {
        $errors = [];
        $name = trim((string) ($input['name'] ?? ''));
        if ($name === '' || mb_strlen($name) > 60) {
            $errors['name'] = 'Enter a name (up to 60 characters).';
        }
        $desc = trim((string) ($input['description'] ?? ''));
        if (mb_strlen($desc) > 300) {
            $errors['description'] = 'Keep the description under 300 characters.';
        }
        $vals = [];
        foreach (array_keys(self::FIELDS) as $f) {
            $v = filter_var($input[$f] ?? 0, FILTER_VALIDATE_INT);
            if ($v === false || $v < 0 || $v > 10_000_000) {
                $errors[$f] = 'Enter 0 (unlimited) or a positive number.';
                continue;
            }
            $vals[$f] = $v;
        }
        $ownerId = Scope::isAll($actor) ? null : (int) $actor['id'];
        if (!Scope::isAll($actor)) {
            $vals['max_customers'] = 0; // customers of resellers cannot resell
        }
        // Feature lists are chosen by administrators; resellers' plans are
        // bounded by the reseller's own plan instead.
        $featureList = null;
        if (Scope::isAll($actor) && ($input['feature_list_id'] ?? '') !== '') {
            $featureList = (int) $input['feature_list_id'];
            if ($this->db->scalar('SELECT id FROM feature_lists WHERE id = ?', [$featureList]) === null) {
                $errors['feature_list_id'] = 'Choose an existing feature list.';
            }
        }
        $dup = $this->db->scalar('SELECT id FROM plans WHERE COALESCE(owner_id, 0) = ? AND name = ? AND id != ?', [$ownerId ?? 0, $name, $id ?? 0]);
        if ($dup !== null) {
            $errors['name'] = 'You already have a plan with this name.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $now = gmdate('c');
        $cols = array_keys(self::FIELDS);
        if ($id === null) {
            $this->db->run(
                'INSERT INTO plans (owner_id, name, description, ' . implode(', ', $cols) . ', created_at, updated_at) VALUES (?, ?, ?, ' . implode(', ', array_fill(0, count($cols), '?')) . ', ?, ?)',
                array_merge([$ownerId, $name, $desc], array_map(static fn (string $c): int => $vals[$c], $cols), [$now, $now])
            );
            $id = $this->db->lastInsertId();
        } else {
            if ($this->find($actor, $id) === null) {
                throw new UserFacingException('Plan not found.');
            }
            $this->db->run(
                'UPDATE plans SET name = ?, description = ?, ' . implode(', ', array_map(static fn (string $c): string => $c . ' = ?', $cols)) . ', updated_at = ? WHERE id = ?',
                array_merge([$name, $desc], array_map(static fn (string $c): int => $vals[$c], $cols), [$now, $id])
            );
        }
        if (Scope::isAll($actor)) {
            $this->db->run('UPDATE plans SET feature_list_id = ? WHERE id = ?', [$featureList, $id]);
        }
        $this->audit->record('plan.save', 'plan', (string) $id, AuditLogger::SUCCESS, ['name' => $name, 'feature_list_id' => $featureList] + $vals);
        return $id;
    }

    /** @param array<string, mixed> $actor */
    public function delete(array $actor, int $id): void
    {
        $p = $this->find($actor, $id) ?? throw new UserFacingException('Plan not found.');
        if ((int) $p['users'] > 0) {
            throw new UserFacingException('This plan is still assigned to ' . (int) $p['users'] . ' user(s).', '', 'Move them to another plan first.');
        }
        $this->db->run('DELETE FROM plans WHERE id = ?', [$id]);
        $this->audit->record('plan.delete', 'plan', (string) $id, AuditLogger::SUCCESS, ['name' => $p['name']]);
    }
}
