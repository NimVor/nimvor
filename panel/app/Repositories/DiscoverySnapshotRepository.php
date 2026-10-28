<?php

declare(strict_types=1);

namespace NimVor\Repositories;

use NimVor\Database\Connection;

final class DiscoverySnapshotRepository
{
    public function __construct(private readonly Connection $db)
    {
    }

    /** @return array{id: int, created_at: string, created_by: ?int, website_count: int, checksum: string, data: array<string, mixed>}|null */
    public function latest(int $serverId = 1): ?array
    {
        $row = $this->db->fetch('SELECT * FROM discovery_snapshots WHERE server_id = ? ORDER BY id DESC LIMIT 1', [$serverId]);
        if ($row === null) {
            return null;
        }
        $data = json_decode((string) $row['data'], true);
        return [
            'id' => (int) $row['id'],
            'created_at' => (string) $row['created_at'],
            'created_by' => $row['created_by'] === null ? null : (int) $row['created_by'],
            'website_count' => (int) $row['website_count'],
            'checksum' => (string) $row['checksum'],
            'data' => is_array($data) ? $data : [],
        ];
    }

    /** @param array<string, mixed> $data */
    public function store(array $data, int $websiteCount, ?int $userId, int $keep = 20, int $serverId = 1): int
    {
        $json = json_encode($data, JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR);
        $this->db->run(
            'INSERT INTO discovery_snapshots (server_id, created_at, created_by, website_count, checksum, data) VALUES (?, ?, ?, ?, ?, ?)',
            [$serverId, gmdate('c'), $userId, $websiteCount, hash('sha256', $json), $json]
        );
        $id = $this->db->lastInsertId();
        $this->db->run(
            'DELETE FROM discovery_snapshots WHERE server_id = ? AND id NOT IN (SELECT id FROM discovery_snapshots WHERE server_id = ? ORDER BY id DESC LIMIT ?)',
            [$serverId, $serverId, $keep]
        );
        return $id;
    }

    /** @return list<array<string, mixed>> */
    public function history(int $limit = 20, int $serverId = 1): array
    {
        return $this->db->fetchAll(
            'SELECT s.id, s.created_at, s.website_count, s.checksum, u.username FROM discovery_snapshots s LEFT JOIN users u ON u.id = s.created_by WHERE s.server_id = ? ORDER BY s.id DESC LIMIT ?',
            [$serverId, $limit]
        );
    }
}
