<?php

declare(strict_types=1);

namespace NimVor\Repositories;

use NimVor\Database\Connection;

/** SQL for website migrations. */
final class MigrationRepository
{
    /** Columns that hold JSON. */
    private const JSON = ['pending_key', 'info', 'discovery', 'selection', 'plan', 'assessment'];

    /** Columns the service may change. */
    private const SETTABLE = ['status', 'source_host', 'source_port', 'source_user', 'sealed', 'pending_key', 'info', 'discovery', 'selection', 'plan', 'assessment', 'agent_id', 'job_id', 'error', 'finished_at'];

    public function __construct(private readonly Connection $db)
    {
    }

    public function insert(int $serverId, string $panel, ?int $userId): int
    {
        $now = gmdate('c');
        $this->db->run('INSERT INTO site_migrations (server_id, panel, status, user_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)', [$serverId, $panel, 'draft', $userId, $now, $now]);
        return $this->db->lastInsertId();
    }

    /** @return array<string, mixed>|null */
    public function find(int $serverId, int $id): ?array
    {
        $r = $this->db->fetch('SELECT * FROM site_migrations WHERE server_id = ? AND id = ?', [$serverId, $id]);
        return $r === null ? null : $this->hydrate($r);
    }

    /** @return list<array<string, mixed>> */
    public function list(int $serverId, int $limit = 200): array
    {
        return array_map([$this, 'hydrate'], $this->db->fetchAll('SELECT * FROM site_migrations WHERE server_id = ? ORDER BY id DESC LIMIT ?', [$serverId, $limit]));
    }

    /** @param array<string, mixed> $fields */
    public function update(int $id, array $fields): void
    {
        $sets = [];
        $params = [];
        foreach ($fields as $k => $v) {
            if (!in_array($k, self::SETTABLE, true)) {
                throw new \InvalidArgumentException('Unknown column ' . $k);
            }
            $sets[] = $k . ' = ?';
            $params[] = in_array($k, self::JSON, true) && !is_string($v) ? json_encode($v, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_THROW_ON_ERROR) : $v;
        }
        $sets[] = 'updated_at = ?';
        $params[] = gmdate('c');
        $params[] = $id;
        $this->db->run('UPDATE site_migrations SET ' . implode(', ', $sets) . ' WHERE id = ?', $params);
    }

    public function delete(int $id): void
    {
        $this->db->run('DELETE FROM site_migrations WHERE id = ?', [$id]);
    }

    /**
     * @param array<string, mixed> $r
     * @return array<string, mixed>
     */
    private function hydrate(array $r): array
    {
        foreach (self::JSON as $k) {
            $r[$k] = json_decode((string) ($r[$k] ?? '{}'), true) ?: [];
        }
        $r['id'] = (int) $r['id'];
        $r['source_port'] = (int) $r['source_port'];
        $r['has_login'] = (string) $r['sealed'] !== '';
        unset($r['sealed']); // the encrypted login never leaves the repository's callers by accident
        return $r;
    }

    /** The encrypted login of a migration ("" when none is kept). */
    public function sealed(int $id): string
    {
        return (string) $this->db->scalar('SELECT sealed FROM site_migrations WHERE id = ?', [$id]);
    }
}
