<?php

declare(strict_types=1);

namespace NimVor\Repositories;

use NimVor\Database\Connection;

/** SQL for the files fetched from remote servers. */
final class FetchedFileRepository
{
    public function __construct(private readonly Connection $db)
    {
    }

    /** @return list<array<string, mixed>> */
    public function list(int $serverId, int $limit = 500): array
    {
        return $this->db->fetchAll('SELECT * FROM fetched_files WHERE server_id = ? ORDER BY id DESC LIMIT ?', [$serverId, $limit]);
    }

    /** @return list<array<string, mixed>> */
    public function running(int $serverId): array
    {
        return $this->db->fetchAll("SELECT * FROM fetched_files WHERE server_id = ? AND status = 'running'", [$serverId]);
    }

    /**
     * @param list<int> $ids
     * @return list<array<string, mixed>>
     */
    public function findMany(int $serverId, array $ids): array
    {
        if ($ids === []) {
            return [];
        }
        $marks = implode(',', array_fill(0, count($ids), '?'));
        return $this->db->fetchAll("SELECT * FROM fetched_files WHERE server_id = ? AND id IN ({$marks}) ORDER BY id", [$serverId, ...$ids]);
    }

    public function count(int $serverId): int
    {
        return (int) $this->db->scalar('SELECT COUNT(*) FROM fetched_files WHERE server_id = ?', [$serverId]);
    }

    public function insert(int $serverId, string $protocol, string $host, int $port, string $username, string $remotePath, string $jobId, ?int $userId): int
    {
        $this->db->run(
            'INSERT INTO fetched_files (server_id, protocol, host, port, username, remote_path, status, job_id, user_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
            [$serverId, $protocol, $host, $port, $username, $remotePath, 'running', $jobId, $userId, gmdate('c')]
        );
        return (int) $this->db->lastInsertId();
    }

    public function complete(int $id, string $fileName, int $size, string $sha256): void
    {
        $this->db->run("UPDATE fetched_files SET status = 'completed', file_name = ?, size = ?, sha256 = ?, finished_at = ? WHERE id = ?", [$fileName, $size, $sha256, gmdate('c'), $id]);
    }

    public function fail(int $id, string $error): void
    {
        $this->db->run("UPDATE fetched_files SET status = 'failed', error = ?, finished_at = ? WHERE id = ?", [mb_substr($error, 0, 500), gmdate('c'), $id]);
    }

    /** @param list<int> $ids */
    public function delete(array $ids): void
    {
        foreach ($ids as $id) {
            $this->db->run('DELETE FROM fetched_files WHERE id = ?', [$id]);
        }
    }
}
