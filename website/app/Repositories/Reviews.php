<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

use NimVorSite\Core\Db;

/** What people say about NimVor. Only published reviews are public; a visitor's review waits for approval. */
final class Reviews
{
    public const SOURCES = ['visitor', 'trustpilot', 'manual'];
    public const STATUSES = ['pending', 'published', 'hidden'];

    public function __construct(private readonly Db $db)
    {
    }

    /** @return list<array<string, mixed>> */
    public function published(int $limit = 100): array
    {
        return $this->db->all("SELECT * FROM reviews WHERE status = 'published' ORDER BY created_at DESC, id DESC LIMIT " . max(1, $limit));
    }

    /** @return array{count: int, average: float} */
    public function summary(): array
    {
        $r = $this->db->one("SELECT COUNT(*) AS n, AVG(rating) AS a FROM reviews WHERE status = 'published'") ?? [];
        return ['count' => (int) ($r['n'] ?? 0), 'average' => round((float) ($r['a'] ?? 0), 1)];
    }

    /** @return list<array<string, mixed>> pending first, then the rest, newest first */
    public function all(): array
    {
        return $this->db->all("SELECT * FROM reviews ORDER BY FIELD(status, 'pending', 'published', 'hidden'), created_at DESC, id DESC");
    }

    public function pendingCount(): int
    {
        return (int) $this->db->scalar("SELECT COUNT(*) FROM reviews WHERE status = 'pending'");
    }

    public function add(string $name, string $role, int $rating, string $title, string $body, string $source, string $url, string $status): int
    {
        $this->db->run(
            'INSERT INTO reviews (name, role, rating, title, body, source, url, status, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP())',
            [$name, $role, $rating, $title, $body, $source, $url, $status]
        );
        return $this->db->lastId();
    }

    public function setStatus(int $id, string $status): void
    {
        $this->db->run('UPDATE reviews SET status = ? WHERE id = ?', [$status, $id]);
    }

    public function find(int $id): ?array
    {
        return $this->db->one('SELECT * FROM reviews WHERE id = ?', [$id]);
    }

    public function delete(int $id): void
    {
        $this->db->run('DELETE FROM reviews WHERE id = ?', [$id]);
    }
}
