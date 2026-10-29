<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

use NimVorSite\Core\Db;

final class Docs
{
    public function __construct(private readonly Db $db)
    {
    }

    /** @return list<array<string, mixed>> every page without its text, for the sidebar */
    public function menu(): array
    {
        return $this->db->all('SELECT slug, grp, sort_order, label, title FROM docs_pages ORDER BY sort_order, slug');
    }

    /** @return array<string, mixed>|null */
    public function bySlug(string $slug): ?array
    {
        return $this->db->one('SELECT * FROM docs_pages WHERE slug = ?', [$slug]);
    }

    public function count(): int
    {
        return (int) $this->db->scalar('SELECT COUNT(*) FROM docs_pages');
    }

    /** @return array<string, string> source path => sha */
    public function shas(): array
    {
        return array_column($this->db->all('SELECT source_path, sha FROM docs_pages'), 'sha', 'source_path');
    }

    /** @return array<string, string> source path => slug */
    public function slugs(): array
    {
        return array_column($this->db->all('SELECT source_path, slug FROM docs_pages'), 'slug', 'source_path');
    }

    /** @param array{slug: string, group: string, order: int, label: string} $entry */
    public function saveSource(string $path, string $sha, array $entry, string $body): void
    {
        $this->db->run(
            "INSERT INTO docs_pages (slug, source_path, sha, grp, sort_order, label, title, body, html, toc, render_v, synced_at)
             VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', '[]', 0, UTC_TIMESTAMP())
             ON DUPLICATE KEY UPDATE slug = VALUES(slug), sha = VALUES(sha), grp = VALUES(grp), sort_order = VALUES(sort_order), label = VALUES(label), body = VALUES(body), render_v = 0, synced_at = UTC_TIMESTAMP()",
            [$entry['slug'], $path, $sha, $entry['group'], $entry['order'], $entry['label'], $entry['label'], $body]
        );
    }

    /** @param list<array{level: int, id: string, text: string}> $toc */
    public function saveRendered(int $id, string $title, string $html, array $toc, int $version): void
    {
        $this->db->run('UPDATE docs_pages SET title = ?, html = ?, toc = ?, render_v = ? WHERE id = ?', [$title, $html, json_encode($toc, JSON_UNESCAPED_UNICODE), $version, $id]);
    }

    /** @return list<array<string, mixed>> */
    public function allWithBody(): array
    {
        return $this->db->all('SELECT id, source_path, body FROM docs_pages');
    }

    /** @param list<string> $keep source paths that stay */
    public function deleteExcept(array $keep): int
    {
        if ($keep === []) {
            return 0;
        }
        $in = implode(',', array_fill(0, count($keep), '?'));
        return $this->db->run("DELETE FROM docs_pages WHERE source_path NOT IN ($in)", $keep);
    }

    /** @return list<array<string, mixed>> */
    public function search(string $q, int $limit = 30): array
    {
        $like = '%' . addcslashes($q, '%_\\') . '%';
        return $this->db->all('SELECT slug, label, title, grp, body FROM docs_pages WHERE title LIKE ? OR body LIKE ? ORDER BY (title LIKE ?) DESC, sort_order LIMIT ' . (int) $limit, [$like, $like, $like]);
    }
}
