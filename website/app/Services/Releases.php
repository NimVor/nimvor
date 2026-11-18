<?php

declare(strict_types=1);

namespace NimVorSite\Services;

use NimVorSite\Cache\Cache;
use NimVorSite\Support\Markdown;

/**
 * The release history shown on the site: the notes come from the project's own CHANGELOG.md (Keep a
 * Changelog layout), the downloads from the GitHub releases. Read at most every fifteen minutes; when
 * GitHub cannot be reached the last good copy is shown, marked as old.
 */
final class Releases
{
    public const FRESH = 900;
    private const KEEP = 30 * 86400;
    /** Section names in the order they are shown; anything else follows in the order it was written. */
    public const CATEGORIES = ['added' => 'Added', 'changed' => 'Changed', 'fixed' => 'Fixed', 'security' => 'Security', 'removed' => 'Removed', 'deprecated' => 'Deprecated'];

    public function __construct(private readonly GitHub $github, private readonly Cache $cache)
    {
    }

    /**
     * @return array{releases: list<array<string, mixed>>, upcoming: ?array<string, mixed>, fetched_at: ?string, old: bool, error: ?string}
     */
    public function history(): array
    {
        $fresh = $this->cache->get('releases:fresh');
        if (is_array($fresh)) {
            return $fresh;
        }
        try {
            $parsed = self::parse($this->github->raw('CHANGELOG.md'));
            try {
                $published = $this->github->releases();
            } catch (GitHubException $e) {
                error_log('nimvor-site releases (downloads): ' . $e->getMessage());
                $published = [];
            }
            $data = self::merge($parsed, $published);
            $data['fetched_at'] = gmdate('c');
            $this->cache->set('releases:fresh', $data, self::FRESH);
            $this->cache->set('releases:last', $data, self::KEEP);
            return $data;
        } catch (GitHubException $e) {
            error_log('nimvor-site releases: ' . $e->getMessage());
            $last = $this->cache->get('releases:last');
            if (is_array($last)) {
                $last['old'] = true;
                $last['error'] = $e->getMessage();
                $this->cache->set('releases:fresh', $last, 60);
                return $last;
            }
            $empty = ['releases' => [], 'upcoming' => null, 'fetched_at' => null, 'old' => true, 'error' => $e->getMessage()];
            $this->cache->set('releases:fresh', $empty, 60);
            return $empty;
        }
    }

    /** @return array<string, mixed>|null one release, by version number */
    public function find(string $version): ?array
    {
        foreach ($this->history()['releases'] as $r) {
            if ($r['version'] === $version) {
                return $r;
            }
        }
        return null;
    }

    /**
     * Reads a Keep a Changelog file. Each "## [version] — date" starts a release; text before its first
     * "###" is the summary; each "###" is a category whose bullets are the changes.
     *
     * @return list<array{version: string, date: ?string, kind: string, summary: string, sections: list<array{key: string, name: string, items: list<string>}>, count: int}>
     */
    public static function parse(string $changelog): array
    {
        $releases = [];
        $cur = null;
        $section = null;
        $fence = false;
        $flush = static function () use (&$cur, &$section, &$releases): void {
            if ($cur !== null) {
                $releases[] = $cur;
            }
            $cur = null;
            $section = null;
        };
        foreach (explode("\n", str_replace(["\r\n", "\r"], "\n", $changelog)) as $line) {
            if (preg_match('/^\s*(```|~~~)/', $line) === 1) {
                $fence = !$fence;
                continue;
            }
            if ($fence) {
                continue;
            }
            if (preg_match('/^## \[([^\]]+)\](?:\s*[—–-]+\s*(\d{4}-\d{2}-\d{2}))?/u', $line, $m) === 1) {
                $flush();
                $cur = ['version' => trim($m[1]), 'date' => $m[2] ?? null, 'kind' => 'patch', 'summary' => '', 'sections' => [], 'count' => 0];
                continue;
            }
            if ($cur === null) {
                continue;
            }
            if (preg_match('/^### +(.+?)\s*$/', $line, $m) === 1) {
                $name = trim($m[1]);
                $key = strtolower($name);
                $key = isset(self::CATEGORIES[$key]) ? $key : 'x-' . trim((string) preg_replace('/[^a-z0-9]+/', '-', $key), '-');
                $idx = null;
                foreach ($cur['sections'] as $i => $s) {
                    if ($s['key'] === $key) {
                        $idx = $i;
                        break;
                    }
                }
                if ($idx === null) {
                    $cur['sections'][] = ['key' => $key, 'name' => isset(self::CATEGORIES[$key]) ? self::CATEGORIES[$key] : $name, 'items' => []];
                    $idx = count($cur['sections']) - 1;
                }
                $section = $idx;
                continue;
            }
            if (preg_match('/^#{1,6} /', $line) === 1 || preg_match('/^\s*\|/', $line) === 1 || trim($line) === '---') {
                continue; // deeper headings, tables and rules are not part of the summary
            }
            if ($section === null) {
                $cur['summary'] .= ($cur['summary'] !== '' && trim($line) !== '' ? ' ' : '') . trim($line);
                continue;
            }
            if (preg_match('/^[-*] +(.*)$/', $line, $m) === 1) {
                $cur['sections'][$section]['items'][] = trim($m[1]);
                $cur['count']++;
                continue;
            }
            $last = count($cur['sections'][$section]['items']) - 1;
            if (trim($line) === '') {
                continue;
            }
            if (preg_match('/^\s+(?:[-*] +)?(.*)$/', $line, $m) === 1 && $last >= 0) {
                $cur['sections'][$section]['items'][$last] .= ' ' . trim($m[1]);
            } else {
                $cur['sections'][$section]['items'][] = trim($line);
                $cur['count']++;
            }
        }
        $flush();
        $order = array_keys(self::CATEGORIES);
        foreach ($releases as &$r) {
            $r['summary'] = trim($r['summary']);
            usort($r['sections'], static function (array $a, array $b) use ($order): int {
                $pa = array_search($a['key'], $order, true);
                $pb = array_search($b['key'], $order, true);
                return ($pa === false ? 99 : $pa) <=> ($pb === false ? 99 : $pb);
            });
            $r['sections'] = array_values(array_filter($r['sections'], static fn (array $s): bool => $s['items'] !== []));
        }
        unset($r);
        return $releases;
    }

    /**
     * @param list<array<string, mixed>> $parsed
     * @param list<array<string, mixed>> $published
     * @return array{releases: list<array<string, mixed>>, upcoming: ?array<string, mixed>, fetched_at: ?string, old: bool, error: ?string}
     */
    public static function merge(array $parsed, array $published): array
    {
        $byVersion = [];
        foreach ($published as $p) {
            $byVersion[(string) $p['version']] = $p;
        }
        $releases = [];
        $upcoming = null;
        foreach ($parsed as $r) {
            if (strcasecmp($r['version'], 'Unreleased') === 0) {
                $upcoming = $r['count'] > 0 ? $r : null;
                continue;
            }
            $r['kind'] = self::kind($r['version']);
            $pub = $byVersion[$r['version']] ?? null;
            $r['url'] = $pub['url'] ?? null;
            $r['assets'] = $pub['assets'] ?? [];
            $r['published'] = $pub !== null;
            $r['prerelease'] = str_contains($r['version'], '-') || (bool) ($pub['prerelease'] ?? false);
            $releases[] = $r;
        }
        return ['releases' => $releases, 'upcoming' => $upcoming, 'fetched_at' => null, 'old' => false, 'error' => null];
    }

    /** major (x.0.0), minor (x.y.0), patch, or pre for a release candidate. */
    public static function kind(string $version): string
    {
        if (str_contains($version, '-')) {
            return 'pre';
        }
        $p = explode('.', $version) + [0, 0, 0];
        return ((int) $p[1] === 0 && (int) $p[2] === 0) ? 'major' : ((int) $p[2] === 0 ? 'minor' : 'patch');
    }

    /**
     * The headline of one change for the list: its first sentence (usually the bold lead), cut at a word
     * if it is still long, as safe HTML. The whole text is on the release's own page.
     */
    public function brief(string $markdown, int $max = 150): string
    {
        $t = preg_replace('/\[([^\]]+)\]\([^)]*\)/', '$1', $markdown) ?? $markdown;
        $t = str_replace(['**', '__'], '', $t);
        $inCode = false;
        $cut = null;
        $n = strlen($t);
        for ($i = 0; $i < $n; $i++) {
            if ($t[$i] === '`') {
                $inCode = !$inCode;
            } elseif (!$inCode && ($t[$i] === '.' || $t[$i] === '!' || $t[$i] === '?') && ($i + 1 === $n || $t[$i + 1] === ' ') && $i > 12) {
                $cut = $i + 1;
                break;
            }
        }
        $first = trim($cut === null ? $t : substr($t, 0, $cut));
        if (mb_strlen($first) > $max) {
            $first = str_replace('`', '', $first);
            $first = rtrim(mb_substr($first, 0, $max), " ,;:-(") ;
            $first = preg_replace('/\s+\S*$/u', '', $first) ?? $first;
            $first = rtrim($first, " ,;:-(") . '…';
        }
        return $this->inline($first);
    }

    /** One change as safe HTML (code spans, bold and links only; every character is escaped). */
    public function inline(string $markdown): string
    {
        $repo = $this->github->repoUrl();
        $html = (new Markdown(static function (string $target) use ($repo): string {
            if (preg_match('#^https?://#i', $target) === 1) {
                return $target;
            }
            return preg_match('#^[A-Za-z0-9_./-]+$#', $target) === 1 && !str_contains($target, '..') ? $repo . '/blob/main/' . ltrim($target, './') : '';
        }))->render($markdown)['html'];
        return trim(preg_replace('#^<p>(.*)</p>$#s', '$1', trim($html)) ?? $html);
    }
}
