<?php

declare(strict_types=1);

namespace NimVorSite\Support;

/** Reads a YouTube address; only the 11-character video id is ever stored. */
final class YouTube
{
    /** The id in a watch, short, embed, shorts or live link, or a bare id. Anything else gives null. */
    public static function id(string $input): ?string
    {
        $input = trim($input);
        if (preg_match('/^[A-Za-z0-9_-]{11}$/', $input) === 1) {
            return $input;
        }
        if (preg_match('#^https://(?:www\.|m\.|music\.)?(youtube\.com|youtube-nocookie\.com|youtu\.be)(/[^\s]*)?$#i', $input, $m) !== 1) {
            return null;
        }
        $host = strtolower($m[1]);
        $rest = $m[2] ?? '';
        $path = (string) parse_url('https://x' . $rest, PHP_URL_PATH);
        parse_str((string) parse_url('https://x' . $rest, PHP_URL_QUERY), $q);
        $id = null;
        if ($host === 'youtu.be') {
            $id = ltrim($path, '/');
        } elseif (preg_match('#^/(?:embed|shorts|live|v)/([A-Za-z0-9_-]{11})(?:/|$)#', $path, $p) === 1) {
            $id = $p[1];
        } elseif ($path === '/watch' && isset($q['v']) && is_string($q['v'])) {
            $id = $q['v'];
        }
        return $id !== null && preg_match('/^[A-Za-z0-9_-]{11}$/', $id) === 1 ? $id : null;
    }

    public static function embedUrl(string $id): string
    {
        return 'https://www.youtube-nocookie.com/embed/' . $id . '?rel=0';
    }

    public static function watchUrl(string $id): string
    {
        return 'https://www.youtube.com/watch?v=' . $id;
    }
}
