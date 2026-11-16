<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

use NimVorSite\Core\Db;

final class Posts
{
    public function __construct(private readonly Db $db)
    {
    }

    /** @return list<array<string, mixed>> */
    public function published(int $limit = 50): array
    {
        return $this->db->all("SELECT id, slug, title, excerpt, published_at FROM posts WHERE status = 'published' AND published_at <= UTC_TIMESTAMP() ORDER BY published_at DESC LIMIT " . (int) $limit);
    }

    /** @return array<string, mixed>|null */
    public function bySlug(string $slug): ?array
    {
        return $this->db->one("SELECT p.*, u.name AS author FROM posts p LEFT JOIN users u ON u.id = p.author_id WHERE p.slug = ? AND p.status = 'published' AND p.published_at <= UTC_TIMESTAMP()", [$slug]);
    }

    /** @return list<array<string, mixed>> */
    public function all(): array
    {
        return $this->db->all('SELECT id, slug, title, status, published_at, updated_at FROM posts ORDER BY updated_at DESC');
    }

    /** @return array<string, mixed>|null */
    public function find(int $id): ?array
    {
        return $this->db->one('SELECT * FROM posts WHERE id = ?', [$id]);
    }

    public function slugTaken(string $slug, ?int $exceptId = null): bool
    {
        return (int) $this->db->scalar('SELECT COUNT(*) FROM posts WHERE slug = ? AND id <> ?', [$slug, $exceptId ?? 0]) > 0;
    }

    public function save(?int $id, string $slug, string $title, string $excerpt, string $body, string $status, ?int $authorId): int
    {
        if ($id === null) {
            $this->db->run(
                'INSERT INTO posts (slug, title, excerpt, body, author_id, status, published_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP(), UTC_TIMESTAMP())',
                [$slug, $title, $excerpt, $body, $authorId, $status, $status === 'published' ? gmdate('Y-m-d H:i:s') : null]
            );
            return $this->db->lastId();
        }
        $this->db->run(
            "UPDATE posts SET source_sha = NULL, slug = ?, title = ?, excerpt = ?, body = ?, status = ?, published_at = IF(? = 'published', COALESCE(published_at, UTC_TIMESTAMP()), published_at), updated_at = UTC_TIMESTAMP() WHERE id = ?",
            [$slug, $title, $excerpt, $body, $status, $status, $id]
        );
        return $id;
    }

    public function delete(int $id): void
    {
        $this->db->run('DELETE FROM posts WHERE id = ?', [$id]);
    }

    /** @return array<string, mixed>|null a post by address, draft or not */
    public function bySlugAny(string $slug): ?array
    {
        return $this->db->one('SELECT id, slug, source_sha FROM posts WHERE slug = ?', [$slug]);
    }

    /** Adds or updates a post that comes from the site's content folder, published on the given day. */
    public function importPost(string $slug, string $title, string $excerpt, string $body, string $date, string $sha): void
    {
        $this->db->run(
            "INSERT INTO posts (slug, title, excerpt, body, author_id, status, source_sha, published_at, created_at, updated_at) VALUES (?, ?, ?, ?, NULL, 'published', ?, ?, UTC_TIMESTAMP(), UTC_TIMESTAMP())
             ON DUPLICATE KEY UPDATE title = VALUES(title), excerpt = VALUES(excerpt), body = VALUES(body), status = 'published', source_sha = VALUES(source_sha), updated_at = UTC_TIMESTAMP()",
            [$slug, $title, $excerpt, $body, $sha, $date . ' 09:00:00']
        );
    }
}
