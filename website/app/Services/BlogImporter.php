<?php

declare(strict_types=1);

namespace NimVorSite\Services;

use NimVorSite\Repositories\Posts;
use NimVorSite\Repositories\State;

/**
 * Loads the guides that ship with the site (content/blog/*.md) into the blog. Each file starts with a
 * small header between two "---" lines (title, slug, excerpt, date), then the post's text. A post
 * that is already there is only changed when its file changed, and one an administrator edited
 * in the meantime is left alone unless asked to be replaced.
 */
final class BlogImporter
{
    public function __construct(private readonly Posts $posts, private readonly string $dir, private readonly ?State $state = null)
    {
    }

    /**
     * Loads new or changed guides on their own, so a freshly deployed site shows them without anyone
     * pressing a button. Cheap when nothing changed (one look at the files' names, sizes and dates);
     * never replaces a post an administrator wrote or edited.
     */
    public function auto(): void
    {
        if ($this->state === null) {
            return;
        }
        try {
            $parts = [];
            foreach (glob($this->dir . '/*.md') ?: [] as $f) {
                $parts[] = basename($f) . ':' . (int) filesize($f) . ':' . (int) filemtime($f);
            }
            sort($parts);
            $fingerprint = sha1(implode('|', $parts));
            if ($parts === [] || $this->state->get('blog:guides') === $fingerprint) {
                return;
            }
            $this->import();
            $this->state->set('blog:guides', $fingerprint);
        } catch (\Throwable) {
            // the blog must still open if the guides cannot be loaded right now
        }
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
                $this->posts->importPost($post['slug'], $post['title'], $post['excerpt'], $post['body'], $post['date'], sha1($post['raw']), $post['image']);
                $r['added']++;
            } elseif ((string) ($existing['source_sha'] ?? '') === sha1($post['raw'])) {
                $r['kept']++;
            } elseif ($existing['source_sha'] === null && !$replaceEdited) {
                $r['kept']++; // written by hand under Admin: not ours to replace
            } else {
                $this->posts->importPost($post['slug'], $post['title'], $post['excerpt'], $post['body'], $post['date'], sha1($post['raw']), $post['image']);
                $r['updated']++;
            }
        }
        return $r;
    }

    /** @return array{slug: string, title: string, excerpt: string, image: string, date: string, body: string, raw: string}|null */
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
        $image = Posts::validImage($h['image'] ?? '') ? ($h['image'] ?? '') : '';
        return ['slug' => $h['slug'], 'title' => $h['title'], 'excerpt' => mb_substr($h['excerpt'] ?? '', 0, 400), 'image' => $image, 'date' => $date, 'body' => trim($m[2]), 'raw' => $raw];
    }
}
