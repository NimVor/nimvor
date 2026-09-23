<?php

declare(strict_types=1);

namespace WhaCP\Repositories;

use WhaCP\Database\Connection;
use WhaCP\Security\Permissions;

final class RoleRepository
{
    public function __construct(private readonly Connection $db)
    {
    }

    /** @return list<array<string, mixed>> */
    public function all(): array
    {
        return $this->db->fetchAll(
            'SELECT r.*, (SELECT COUNT(*) FROM users u WHERE u.role_id = r.id) AS user_count FROM roles r ORDER BY r.id'
        );
    }

    /** @return array<string, mixed>|null */
    public function find(int $id): ?array
    {
        return $this->db->fetch('SELECT * FROM roles WHERE id = ?', [$id]);
    }

    /** @return array<string, mixed>|null */
    public function findByName(string $name): ?array
    {
        return $this->db->fetch('SELECT * FROM roles WHERE name = ?', [$name]);
    }

    /** @return list<array<string, mixed>> */
    public function allPermissions(): array
    {
        return $this->db->fetchAll('SELECT name, label, grp FROM permissions ORDER BY grp, name');
    }

    public function create(string $name, string $label, string $description): int
    {
        $this->db->run(
            'INSERT INTO roles (name, label, description, is_system, created_at) VALUES (?, ?, ?, 0, ?)',
            [$name, $label, $description, gmdate('c')]
        );
        return $this->db->lastInsertId();
    }

    public function update(int $id, string $label, string $description): void
    {
        $this->db->run('UPDATE roles SET label = ?, description = ? WHERE id = ? AND is_system = 0', [$label, $description, $id]);
    }

    public function delete(int $id): void
    {
        $this->db->run('DELETE FROM roles WHERE id = ? AND is_system = 0', [$id]);
    }

    /** @param list<string> $permissions */
    public function setPermissions(int $roleId, array $permissions): void
    {
        $this->db->transaction(function (Connection $db) use ($roleId, $permissions): void {
            $db->run('DELETE FROM role_permissions WHERE role_id = ?', [$roleId]);
            foreach ($permissions as $perm) {
                $db->run('INSERT INTO role_permissions (role_id, permission_id) SELECT ?, id FROM permissions WHERE name = ?', [$roleId, $perm]);
            }
        });
    }

    public function userCount(int $roleId): int
    {
        return (int) $this->db->scalar('SELECT COUNT(*) FROM users WHERE role_id = ?', [$roleId]);
    }

    /** @return list<string> */
    public function permissionsFor(int $roleId): array
    {
        $rows = $this->db->fetchAll(
            'SELECT p.name FROM permissions p JOIN role_permissions rp ON rp.permission_id = p.id WHERE rp.role_id = ? ORDER BY p.name',
            [$roleId]
        );
        return array_map(static fn (array $r): string => (string) $r['name'], $rows);
    }

    /**
     * Mirror the code-defined permission catalogue and default roles into
     * the database. Idempotent; custom grants on non-system roles are kept.
     */
    public function syncDefaults(): void
    {
        $this->db->transaction(function (Connection $db): void {
            $catalogue = Permissions::all();
            foreach ($catalogue as $name => $meta) {
                $db->run(
                    'INSERT INTO permissions (name, label, grp) VALUES (?, ?, ?)
                     ON CONFLICT(name) DO UPDATE SET label = excluded.label, grp = excluded.grp',
                    [$name, $meta['label'], $meta['group']]
                );
            }
            $names = array_keys($catalogue);
            $placeholders = implode(',', array_fill(0, count($names), '?'));
            $db->run("DELETE FROM permissions WHERE name NOT IN ({$placeholders})", $names);

            foreach (Permissions::defaultRoles() as $roleName => $role) {
                $db->run(
                    'INSERT INTO roles (name, label, description, is_system, created_at) VALUES (?, ?, ?, 1, ?)
                     ON CONFLICT(name) DO UPDATE SET label = excluded.label, description = excluded.description, is_system = 1',
                    [$roleName, $role['label'], $role['description'], gmdate('c')]
                );
                $roleId = (int) $db->scalar('SELECT id FROM roles WHERE name = ?', [$roleName]);
                $grants = $role['permissions'] === ['*'] ? $names : $role['permissions'];
                $db->run('DELETE FROM role_permissions WHERE role_id = ?', [$roleId]);
                foreach ($grants as $perm) {
                    $db->run(
                        'INSERT INTO role_permissions (role_id, permission_id) SELECT ?, id FROM permissions WHERE name = ?',
                        [$roleId, $perm]
                    );
                }
            }
        });
    }
}
