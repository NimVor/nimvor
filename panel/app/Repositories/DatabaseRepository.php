<?php

declare(strict_types=1);

namespace NimVor\Repositories;

use NimVor\Database\Connection;
use NimVor\Hosting\Scope;

final class DatabaseRepository
{
    public function __construct(private readonly Connection $db)
    {
    }

    /**
     * @param array<string, mixed> $user
     * @return list<array<string, mixed>>
     */
    public function databases(int $serverId, array $user, ?int $websiteId = null): array
    {
        [$cond, $params] = Scope::condition($user, 'd.owner_id');
        $sql = "SELECT d.*, w.domain AS website FROM databases d LEFT JOIN websites w ON w.id = d.website_id
                WHERE d.server_id = :server AND {$cond}" . ($websiteId !== null ? ' AND d.website_id = :site' : '') . ' ORDER BY d.name';
        $params['server'] = $serverId;
        if ($websiteId !== null) {
            $params['site'] = $websiteId;
        }
        $rows = $this->db->fetchAll($sql, $params);
        foreach ($rows as &$r) {
            $r['grants'] = $this->db->fetchAll(
                'SELECT g.privileges, u.id AS user_id, u.username, u.host FROM database_grants g JOIN database_users u ON u.id = g.user_id WHERE g.database_id = ? ORDER BY u.username',
                [(int) $r['id']]
            );
        }
        return $rows;
    }

    /**
     * @param array<string, mixed>|null $user
     * @return array<string, mixed>|null
     */
    public function database(int $serverId, int $id, ?array $user): ?array
    {
        [$cond, $params] = $user === null ? ['1 = 1', []] : Scope::condition($user, 'owner_id');
        return $this->db->fetch("SELECT * FROM databases WHERE server_id = :server AND id = :id AND {$cond}", ['server' => $serverId, 'id' => $id] + $params);
    }

    public function nameTaken(int $serverId, string $name): bool
    {
        return (bool) $this->db->scalar('SELECT 1 FROM databases WHERE server_id = ? AND name = ?', [$serverId, $name]);
    }

    /**
     * @param array<string, mixed> $user
     * @return list<array<string, mixed>>
     */
    public function users(int $serverId, array $user): array
    {
        [$cond, $params] = Scope::condition($user, 'owner_id');
        return $this->db->fetchAll("SELECT id, server_id, owner_id, username, host, password_enc != '' AS stored, created_at FROM database_users WHERE server_id = :server AND {$cond} ORDER BY username", ['server' => $serverId] + $params);
    }

    /**
     * @param array<string, mixed>|null $user
     * @return array<string, mixed>|null
     */
    public function user(int $serverId, int $id, ?array $user): ?array
    {
        [$cond, $params] = $user === null ? ['1 = 1', []] : Scope::condition($user, 'owner_id');
        return $this->db->fetch("SELECT * FROM database_users WHERE server_id = :server AND id = :id AND {$cond}", ['server' => $serverId, 'id' => $id] + $params);
    }

    public function userTaken(int $serverId, string $name, string $host): bool
    {
        return (bool) $this->db->scalar('SELECT 1 FROM database_users WHERE server_id = ? AND username = ? AND host = ?', [$serverId, $name, $host]);
    }

    public function insertDatabase(int $serverId, string $name, ?int $websiteId, ?int $ownerId): int
    {
        $this->db->run('INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (?, ?, ?, ?, ?)', [$serverId, $websiteId, $ownerId, $name, gmdate('c')]);
        return $this->db->lastInsertId();
    }

    public function insertUser(int $serverId, string $name, string $host, ?int $ownerId, string $passwordEnc): int
    {
        $this->db->run('INSERT INTO database_users (server_id, owner_id, username, host, password_enc, created_at) VALUES (?, ?, ?, ?, ?, ?)', [$serverId, $ownerId, $name, $host, $passwordEnc, gmdate('c')]);
        return $this->db->lastInsertId();
    }

    public function setPasswordEnc(int $userId, string $enc): void
    {
        $this->db->run('UPDATE database_users SET password_enc = ? WHERE id = ?', [$enc, $userId]);
    }

    /** @return list<array{database_id: int, name: string, privileges: string}> */
    public function grantsOf(int $userId): array
    {
        return $this->db->fetchAll('SELECT g.database_id, d.name, g.privileges FROM database_grants g JOIN databases d ON d.id = g.database_id WHERE g.user_id = ? ORDER BY d.name', [$userId]);
    }

    public function setGrant(int $databaseId, int $userId, ?string $privileges): void
    {
        if ($privileges === null) {
            $this->db->run('DELETE FROM database_grants WHERE database_id = ? AND user_id = ?', [$databaseId, $userId]);
            return;
        }
        $this->db->run('INSERT INTO database_grants (database_id, user_id, privileges) VALUES (?, ?, ?) ON CONFLICT(database_id, user_id) DO UPDATE SET privileges = excluded.privileges', [$databaseId, $userId, $privileges]);
    }

    /** @return list<int> */
    public function userIdsOf(int $databaseId): array
    {
        return array_map(static fn (array $r): int => (int) $r['user_id'], $this->db->fetchAll('SELECT user_id FROM database_grants WHERE database_id = ?', [$databaseId]));
    }

    public function deleteDatabase(int $id): void
    {
        $this->db->run('DELETE FROM databases WHERE id = ?', [$id]);
    }

    public function deleteUser(int $id): void
    {
        $this->db->run('DELETE FROM database_users WHERE id = ?', [$id]);
    }

    /** @return list<array<string, mixed>> */
    public function forWebsite(int $websiteId): array
    {
        return $this->db->fetchAll('SELECT * FROM databases WHERE website_id = ?', [$websiteId]);
    }
}
