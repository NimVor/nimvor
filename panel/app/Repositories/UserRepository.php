<?php

declare(strict_types=1);

namespace WhaCP\Repositories;

use WhaCP\Database\Connection;

final class UserRepository
{
    private const COLUMNS = 'u.id, u.username, u.email, u.display_name, u.role_id, u.is_active, u.last_login_at, u.last_login_ip, u.password_changed_at, u.created_at, u.updated_at, r.name AS role_name, r.label AS role_label, r.scope AS role_scope, r.require_two_factor AS role_requires_2fa, u.parent_id, u.plan_id, u.suspended_at, u.suspend_reason, ' . self::SUSPENDED . ' AS suspended';

    /** An account is suspended itself or through its (suspended) reseller. */
    /** The account (or its reseller) is over its monthly bandwidth this month. */
    public const BANDWIDTH_BLOCKED = "(u.bw_blocked_month = strftime('%Y-%m', 'now') OR EXISTS (SELECT 1 FROM users bp WHERE bp.id = u.parent_id AND bp.bw_blocked_month = strftime('%Y-%m', 'now')))";
    public const SUSPENDED = '(u.suspended_at IS NOT NULL OR EXISTS (SELECT 1 FROM users sp WHERE sp.id = u.parent_id AND sp.suspended_at IS NOT NULL))';

    /**
     * Whether an account may sign in and act: active and not suspended.
     *
     * @param array<string, mixed>|null $user
     */
    public static function canAct(?array $user): bool
    {
        return $user !== null && (int) ($user['is_active'] ?? 0) === 1 && empty($user['suspended']);
    }

    public function __construct(private readonly Connection $db)
    {
    }

    /** @return list<array<string, mixed>> */
    public function all(): array
    {
        return $this->db->fetchAll('SELECT ' . self::COLUMNS . ' FROM users u JOIN roles r ON r.id = u.role_id ORDER BY u.username');
    }

    /**
     * Users an actor may manage: everyone for server-wide roles, only
     * their own customers for resellers.
     *
     * @param array<string, mixed> $actor
     * @return list<array<string, mixed>>
     */
    public function manageableBy(array $actor): array
    {
        if (($actor['role_scope'] ?? 'all') === 'all') {
            return $this->db->fetchAll('SELECT ' . self::COLUMNS . ', p.name AS plan_name FROM users u JOIN roles r ON r.id = u.role_id LEFT JOIN plans p ON p.id = u.plan_id ORDER BY u.username');
        }
        return $this->db->fetchAll('SELECT ' . self::COLUMNS . ', p.name AS plan_name FROM users u JOIN roles r ON r.id = u.role_id LEFT JOIN plans p ON p.id = u.plan_id WHERE u.parent_id = ? ORDER BY u.username', [(int) $actor['id']]);
    }

    public function setOwnership(int $id, ?int $parentId, ?int $planId): void
    {
        $this->db->run('UPDATE users SET parent_id = ?, plan_id = ? WHERE id = ?', [$parentId, $planId, $id]);
    }

    /** @return array<string, mixed>|null */
    public function find(int $id): ?array
    {
        return $this->db->fetch('SELECT ' . self::COLUMNS . ' FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = ?', [$id]);
    }

    /**
     * Includes the password hash; only for authentication.
     *
     * @return array<string, mixed>|null
     */
    public function findForAuth(string $username): ?array
    {
        return $this->db->fetch(
            'SELECT u.id, u.username, u.password_hash, u.is_active, u.role_id, ' . self::SUSPENDED . ' AS suspended FROM users u WHERE u.username = ? OR u.email = ?',
            [$username, $username]
        );
    }

    public function passwordHash(int $id): ?string
    {
        $v = $this->db->scalar('SELECT password_hash FROM users WHERE id = ?', [$id]);
        return is_string($v) ? $v : null;
    }

    public function usernameExists(string $username, ?int $exceptId = null): bool
    {
        return (bool) $this->db->scalar('SELECT 1 FROM users WHERE username = ? AND id != ?', [$username, $exceptId ?? 0]);
    }

    public function emailExists(string $email, ?int $exceptId = null): bool
    {
        return (bool) $this->db->scalar('SELECT 1 FROM users WHERE email = ? AND id != ?', [$email, $exceptId ?? 0]);
    }

    public function count(): int
    {
        return (int) $this->db->scalar('SELECT COUNT(*) FROM users');
    }

    public function countActiveWithRole(string $roleName): int
    {
        return (int) $this->db->scalar(
            'SELECT COUNT(*) FROM users u JOIN roles r ON r.id = u.role_id WHERE r.name = ? AND u.is_active = 1',
            [$roleName]
        );
    }

    /** @param array{username: string, email: string, display_name: string, password_hash: string, role_id: int, is_active: bool} $data */
    public function create(array $data): int
    {
        $now = gmdate('c');
        $this->db->run(
            'INSERT INTO users (username, email, display_name, password_hash, role_id, is_active, password_changed_at, created_at, updated_at)
             VALUES (:username, :email, :display_name, :password_hash, :role_id, :is_active, :now, :now, :now)',
            [
                'username' => $data['username'],
                'email' => $data['email'],
                'display_name' => $data['display_name'],
                'password_hash' => $data['password_hash'],
                'role_id' => $data['role_id'],
                'is_active' => $data['is_active'] ? 1 : 0,
                'now' => $now,
            ]
        );
        return $this->db->lastInsertId();
    }

    /** @param array{email: string, display_name: string, role_id: int, is_active: bool} $data */
    public function update(int $id, array $data): void
    {
        $this->db->run(
            'UPDATE users SET email = :email, display_name = :display_name, role_id = :role_id, is_active = :is_active, updated_at = :now WHERE id = :id',
            [
                'email' => $data['email'],
                'display_name' => $data['display_name'],
                'role_id' => $data['role_id'],
                'is_active' => $data['is_active'] ? 1 : 0,
                'now' => gmdate('c'),
                'id' => $id,
            ]
        );
    }

    public function updatePassword(int $id, string $hash): void
    {
        $now = gmdate('c');
        $this->db->run('UPDATE users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?', [$hash, $now, $now, $id]);
    }

    public function recordLogin(int $id, string $ip): void
    {
        $this->db->run('UPDATE users SET last_login_at = ?, last_login_ip = ? WHERE id = ?', [gmdate('c'), $ip, $id]);
    }

    public function delete(int $id): void
    {
        $this->db->run('DELETE FROM users WHERE id = ?', [$id]);
    }
}
