<?php

declare(strict_types=1);

namespace NimVor\Repositories;

use NimVor\Database\Connection;
use NimVor\Hosting\Scope;

/** Who owns which PostgreSQL database and user (the server holds the data; this holds the ownership). */
final class PostgresRepository
{
    public function __construct(private readonly Connection $db)
    {
    }

    /** The row of this managed server's local PostgreSQL, created on first use. */
    public function localServerId(int $serverId): int
    {
        $id = $this->db->scalar("SELECT id FROM pg_servers WHERE server_id = ? AND connection_type = 'local' ORDER BY id LIMIT 1", [$serverId]);
        if ($id !== null && $id !== false) {
            return (int) $id;
        }
        $now = gmdate('c');
        $this->db->run("INSERT INTO pg_servers (server_id, name, connection_type, host, port, created_at, updated_at) VALUES (?, 'Local PostgreSQL', 'local', '127.0.0.1', 5432, ?, ?)", [$serverId, $now, $now]);
        return (int) $this->db->lastInsertId();
    }

    /**
     * @param array<string, mixed> $user
     * @return list<array<string, mixed>>
     */
    public function databases(int $pgServer, array $user, ?int $websiteId = null): array
    {
        [$cond, $params] = Scope::condition($user, 'd.owner_id');
        $sql = "SELECT d.*, w.domain AS website, r.name AS owner_role FROM pg_databases d
                LEFT JOIN websites w ON w.id = d.website_id LEFT JOIN pg_roles r ON r.id = d.owner_role_id
                WHERE d.pg_server_id = :s AND {$cond}" . ($websiteId !== null ? ' AND d.website_id = :site' : '') . ' ORDER BY d.name';
        $params['s'] = $pgServer;
        if ($websiteId !== null) {
            $params['site'] = $websiteId;
        }
        $rows = $this->db->fetchAll($sql, $params);
        foreach ($rows as &$r) {
            $r['grants'] = $this->db->fetchAll(
                'SELECT g.level, u.id AS role_id, u.name FROM pg_grants g JOIN pg_roles u ON u.id = g.role_id WHERE g.database_id = ? ORDER BY u.name',
                [(int) $r['id']]
            );
        }
        return $rows;
    }

    /**
     * @param array<string, mixed> $user
     * @return list<array<string, mixed>>
     */
    public function roles(int $pgServer, array $user, ?int $websiteId = null): array
    {
        [$cond, $params] = Scope::condition($user, 'r.owner_id');
        $sql = "SELECT r.*, w.domain AS website FROM pg_roles r LEFT JOIN websites w ON w.id = r.website_id
                WHERE r.pg_server_id = :s AND {$cond}" . ($websiteId !== null ? ' AND r.website_id = :site' : '') . ' ORDER BY r.name';
        $params['s'] = $pgServer;
        if ($websiteId !== null) {
            $params['site'] = $websiteId;
        }
        return $this->db->fetchAll($sql, $params);
    }

    /**
     * @param array<string, mixed> $user
     * @return array<string, mixed>|null
     */
    public function database(int $pgServer, int $id, array $user): ?array
    {
        [$cond, $params] = Scope::condition($user, 'owner_id');
        return $this->db->fetch("SELECT * FROM pg_databases WHERE pg_server_id = :s AND id = :id AND {$cond}", ['s' => $pgServer, 'id' => $id] + $params);
    }

    /**
     * @param array<string, mixed> $user
     * @return array<string, mixed>|null
     */
    public function role(int $pgServer, int $id, array $user): ?array
    {
        [$cond, $params] = Scope::condition($user, 'owner_id');
        return $this->db->fetch("SELECT * FROM pg_roles WHERE pg_server_id = :s AND id = :id AND {$cond}", ['s' => $pgServer, 'id' => $id] + $params);
    }

    public function databaseNameTaken(int $pgServer, string $name): bool
    {
        return (bool) $this->db->scalar('SELECT 1 FROM pg_databases WHERE pg_server_id = ? AND name = ?', [$pgServer, $name]);
    }

    public function roleNameTaken(int $pgServer, string $name): bool
    {
        return (bool) $this->db->scalar('SELECT 1 FROM pg_roles WHERE pg_server_id = ? AND name = ?', [$pgServer, $name]);
    }

    public function insertRole(int $pgServer, string $name, ?int $ownerId, ?int $websiteId, string $passwordEnc): int
    {
        $now = gmdate('c');
        $this->db->run('INSERT INTO pg_roles (pg_server_id, owner_id, website_id, name, password_enc, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)', [$pgServer, $ownerId, $websiteId, $name, $passwordEnc, $now, $now]);
        return (int) $this->db->lastInsertId();
    }

    public function insertDatabase(int $pgServer, string $name, ?int $ownerId, ?int $websiteId, int $ownerRoleId, string $encoding): int
    {
        $now = gmdate('c');
        $this->db->run('INSERT INTO pg_databases (pg_server_id, owner_id, website_id, owner_role_id, name, encoding, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)', [$pgServer, $ownerId, $websiteId, $ownerRoleId, $name, $encoding, $now, $now]);
        return (int) $this->db->lastInsertId();
    }

    public function setGrant(int $databaseId, int $roleId, string $level): void
    {
        if ($level === 'none') {
            $this->db->run('DELETE FROM pg_grants WHERE database_id = ? AND role_id = ?', [$databaseId, $roleId]);
            return;
        }
        $this->db->run('INSERT INTO pg_grants (database_id, role_id, level, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT (database_id, role_id) DO UPDATE SET level = excluded.level, updated_at = excluded.updated_at', [$databaseId, $roleId, $level, gmdate('c')]);
    }

    public function setPasswordEnc(int $roleId, string $enc): void
    {
        $this->db->run('UPDATE pg_roles SET password_enc = ?, updated_at = ? WHERE id = ?', [$enc, gmdate('c'), $roleId]);
    }

    public function deleteDatabase(int $id): void
    {
        $this->db->run('DELETE FROM pg_databases WHERE id = ?', [$id]);
    }

    public function deleteRole(int $id): void
    {
        $this->db->run('DELETE FROM pg_roles WHERE id = ?', [$id]);
    }

    /** Databases a role owns, by the panel's records. */
    public function ownedBy(int $roleId): int
    {
        return (int) $this->db->scalar('SELECT COUNT(*) FROM pg_databases WHERE owner_role_id = ?', [$roleId]);
    }

    // ---- SQL editor history ---------------------------------------------------------------------

    public function recordQuery(int $databaseId, ?int $userId, string $fingerprint, string $mode, int $ms, int $rows, string $status): void
    {
        $this->db->run('INSERT INTO pg_query_history (pg_database_id, user_id, fingerprint, mode, duration_ms, rows_returned, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)', [$databaseId, $userId, $fingerprint, $mode, $ms, $rows, $status, gmdate('c')]);
        // Keep the newest 200 per user and database.
        $this->db->run('DELETE FROM pg_query_history WHERE pg_database_id = ? AND user_id IS ? AND id NOT IN (SELECT id FROM pg_query_history WHERE pg_database_id = ? AND user_id IS ? ORDER BY id DESC LIMIT 200)', [$databaseId, $userId, $databaseId, $userId]);
    }

    /** @return list<array<string, mixed>> */
    public function history(int $databaseId, ?int $userId, int $limit = 50): array
    {
        return $this->db->fetchAll('SELECT fingerprint, mode, duration_ms, rows_returned, status, created_at FROM pg_query_history WHERE pg_database_id = ? AND user_id IS ? ORDER BY id DESC LIMIT ' . max(1, min(200, $limit)), [$databaseId, $userId]);
    }

    // ---- backup schedules ----------------------------------------------------------------------------

    /** @return array<string, mixed>|null */
    public function schedule(int $databaseId): ?array
    {
        return $this->db->fetch('SELECT * FROM pg_backup_schedules WHERE pg_database_id = ?', [$databaseId]);
    }

    public function saveSchedule(int $databaseId, string $frequency, int $hour, int $keep): void
    {
        $this->db->run(
            'INSERT INTO pg_backup_schedules (pg_database_id, frequency, hour, keep, created_at) VALUES (?, ?, ?, ?, ?)
             ON CONFLICT(pg_database_id) DO UPDATE SET frequency = excluded.frequency, hour = excluded.hour, keep = excluded.keep',
            [$databaseId, $frequency, $hour, $keep, gmdate('c')]
        );
    }

    public function deleteSchedule(int $databaseId): void
    {
        $this->db->run('DELETE FROM pg_backup_schedules WHERE pg_database_id = ?', [$databaseId]);
    }

    public function markScheduleRun(int $scheduleId, string $status, ?int $at = null): void
    {
        $this->db->run('UPDATE pg_backup_schedules SET last_run_at = ?, last_status = ? WHERE id = ?', [gmdate('c', $at ?? time()), $status, $scheduleId]);
    }

    /** @return list<array<string, mixed>> every schedule of this managed server's databases, with the database name */
    public function schedulesOf(int $pgServer): array
    {
        return $this->db->fetchAll(
            'SELECT s.*, d.name AS database_name, d.id AS database_id FROM pg_backup_schedules s JOIN pg_databases d ON d.id = s.pg_database_id WHERE d.pg_server_id = ? ORDER BY s.id',
            [$pgServer]
        );
    }
}
