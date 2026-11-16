<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

use NimVorSite\Core\Db;

final class Videos
{
    public function __construct(private readonly Db $db)
    {
    }

    /** @return list<array<string, mixed>> published videos, grouped by the caller */
    public function published(): array
    {
        return $this->db->all('SELECT * FROM videos WHERE published = 1 ORDER BY category, sort_order, id DESC');
    }

    /** @return list<array<string, mixed>> */
    public function all(): array
    {
        return $this->db->all('SELECT * FROM videos ORDER BY category, sort_order, id DESC');
    }

    /** @return array<string, mixed>|null */
    public function find(int $id): ?array
    {
        return $this->db->one('SELECT * FROM videos WHERE id = ?', [$id]);
    }

    /** @return array<string, mixed>|null */
    public function bySlug(string $slug): ?array
    {
        return $this->db->one('SELECT * FROM videos WHERE slug = ? AND published = 1', [$slug]);
    }

    /** @return array<string, mixed>|null the video that owns a stored file name (video or poster) */
    public function byFile(string $name): ?array
    {
        return $this->db->one('SELECT * FROM videos WHERE file = ? OR poster = ?', [$name, $name]);
    }

    public function slugTaken(string $slug, ?int $exceptId = null): bool
    {
        return $this->db->one('SELECT id FROM videos WHERE slug = ? AND id <> ?', [$slug, $exceptId ?? 0]) !== null;
    }

    /** @param array<string, mixed> $row */
    public function create(array $row): int
    {
        $this->db->run(
            'INSERT INTO videos (slug, title, description, category, source, youtube_id, file, mime, size_bytes, poster, duration, sort_order, published, created_at)
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, UTC_TIMESTAMP())',
            [$row['slug'], $row['title'], $row['description'], $row['category'], $row['source'], $row['youtube_id'] ?? null, $row['file'] ?? null, $row['mime'] ?? null, $row['size_bytes'] ?? null, $row['poster'] ?? null, $row['duration'] ?? '', $row['sort_order'] ?? 100, $row['published'] ?? 1]
        );
        return (int) $this->db->scalar('SELECT LAST_INSERT_ID()');
    }

    public function update(int $id, string $slug, string $title, string $description, string $category, string $duration, int $sortOrder, bool $published): void
    {
        $this->db->run(
            'UPDATE videos SET slug = ?, title = ?, description = ?, category = ?, duration = ?, sort_order = ?, published = ? WHERE id = ?',
            [$slug, $title, $description, $category, $duration, $sortOrder, $published ? 1 : 0, $id]
        );
    }

    public function setPoster(int $id, ?string $poster): void
    {
        $this->db->run('UPDATE videos SET poster = ? WHERE id = ?', [$poster, $id]);
    }

    public function setPublished(int $id, bool $published): void
    {
        $this->db->run('UPDATE videos SET published = ? WHERE id = ?', [$published ? 1 : 0, $id]);
    }

    public function delete(int $id): void
    {
        $this->db->run('DELETE FROM videos WHERE id = ?', [$id]);
    }
}
