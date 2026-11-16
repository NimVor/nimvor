<?php

declare(strict_types=1);

namespace NimVorSite\Services;

use NimVorSite\Repositories\Posts;

/**
 * Loads the guides that ship with the site (content/blog/*.md) into the blog. Each file starts with a
 * small header between two "---" lines (title, slug, excerpt, date), then the post's text. A post
 * that is already there is only changed when its file changed, and one an administrator edited
 * in the meantime is left alone unless asked to be replaced.
 */
final class BlogImporter
{
    public function __construct(private readonly Posts $posts, private readonly string $dir)
    {
    }

    /**
     * @return array{added: int, updated: int, kept: int}
     */
    public function import(bool $replaceEdited = false): array
    {
        $r = ['added' => 0, 'updated' => 0, 'kept' => 0];
        $files = glob($this->dir . '/*.md') ?: [];
        sort($files);
        foreach ($files as $file) {
            $post = self::parse((string) file_get_contents($file));
            if ($post === null) {
                continue;
            }
            $existing = $this->posts->bySlugAny($post['slug']);
            if ($existing === null) {
                $this->posts->importPost($post['slug'], $post['title'], $post['excerpt'], $post['body'], $post['date'], sha1($post['raw']));
                $r['added']++;
            } elseif ((string) ($existing['source_sha'] ?? '') === sha1($post['raw'])) {
                $r['kept']++;
            } elseif ($existing['source_sha'] === null && !$replaceEdited) {
                $r['kept']++; // written by hand under Admin: not ours to replace
            } else {
                $this->posts->importPost($post['slug'], $post['title'], $post['excerpt'], $post['body'], $post['date'], sha1($post['raw']));
                $r['updated']++;
            }
        }
        return $r;
    }

    /** @return array{slug: string, title: string, excerpt: string, date: string, body: string, raw: string}|null */
    public static function parse(string $raw): ?array
    {
        $raw = str_replace("\r\n", "\n", $raw);
        if (preg_match('/\A---\n(.*?)\n---\n(.*)\z/s', $raw, $m) !== 1) {
            return null;
        }
        $h = [];
        foreach (explode("\n", $m[1]) as $line) {
            if (preg_match('/^([a-z]+):\s*(.*)$/', $line, $kv) === 1) {
                $h[$kv[1]] = trim($kv[2]);
            }
        }
        if (!isset($h['title'], $h['slug']) || preg_match('/^[a-z0-9][a-z0-9-]{0,99}$/', $h['slug']) !== 1 || mb_strlen($h['title']) > 200) {
            return null;
        }
        $date = isset($h['date']) && preg_match('/^\d{4}-\d{2}-\d{2}$/', $h['date']) === 1 ? $h['date'] : gmdate('Y-m-d');
        return ['slug' => $h['slug'], 'title' => $h['title'], 'excerpt' => mb_substr($h['excerpt'] ?? '', 0, 400), 'date' => $date, 'body' => trim($m[2]), 'raw' => $raw];
    }
}
