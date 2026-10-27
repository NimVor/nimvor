<?php

declare(strict_types=1);

namespace WhaCP\Services;

use Closure;

/**
 * Known vulnerabilities of an installation's WordPress version, plugins and
 * themes, from the free WPVulnerability.net database (public data, no
 * account; the panel sends only names and version numbers). Answers are
 * cached for twelve hours per item. When the database cannot be reached
 * the check says so instead of reporting "no vulnerabilities".
 */
final class WordPressVulnerabilities
{
    public const API = 'https://www.wpvulnerability.net/';
    private const TTL = 43200;
    private const SEVERITY = ['c' => 'critical', 'h' => 'high', 'm' => 'medium', 'l' => 'low', 'n' => 'none'];

    /** @var Closure(string): ?string */
    private Closure $fetch;

    /** @param (Closure(string): ?string)|null $fetch returns the body, or null when unreachable */
    public function __construct(private readonly string $cacheDir = '', ?Closure $fetch = null)
    {
        $this->fetch = $fetch ?? static function (string $url): ?string {
            $ch = curl_init($url);
            curl_setopt_array($ch, [CURLOPT_RETURNTRANSFER => true, CURLOPT_TIMEOUT => 10, CURLOPT_CONNECTTIMEOUT => 5, CURLOPT_FOLLOWLOCATION => false,
                CURLOPT_PROTOCOLS => CURLPROTO_HTTPS, CURLOPT_USERAGENT => 'NimVor WordPress toolkit', CURLOPT_HTTPHEADER => ['Accept: application/json']]);
            $body = curl_exec($ch);
            $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
            curl_close($ch);
            return is_string($body) && $code === 200 && strlen($body) < 4_000_000 ? $body : null;
        };
    }

    /**
     * @param array<string, mixed> $info the installation overview (version, plugins, themes)
     * @return array{items: list<array<string, mixed>>, reachable: bool}
     */
    public function check(array $info): array
    {
        $items = [];
        $reachable = true;
        $targets = [['core', 'wordpress', (string) ($info['version'] ?? ''), 'WordPress']];
        foreach ((array) ($info['plugins'] ?? []) as $p) {
            if (empty($p['must_use'])) {
                $targets[] = ['plugin', (string) $p['name'], (string) $p['version'], (string) ($p['title'] ?: $p['name'])];
            }
        }
        foreach ((array) ($info['themes'] ?? []) as $t) {
            $targets[] = ['theme', (string) $t['name'], (string) $t['version'], (string) ($t['title'] ?: $t['name'])];
        }
        foreach (array_slice($targets, 0, 300) as [$kind, $slug, $version, $title]) {
            if ($version === '' || !preg_match('/^[A-Za-z0-9._-]{1,100}$/', $slug) || !preg_match('/^[0-9A-Za-z.+-]{1,40}$/', $version)) {
                continue;
            }
            $data = $this->get($kind === 'core' ? 'core/' . rawurlencode($version) . '/' : $kind . '/' . rawurlencode($slug) . '/');
            if ($data === null) {
                $reachable = false;
                continue;
            }
            foreach (self::affecting($data, $kind === 'core' ? null : $version) as $v) {
                $items[] = ['kind' => $kind, 'slug' => $slug, 'title' => $title, 'version' => $version] + $v;
            }
        }
        $rank = ['critical' => 0, 'high' => 1, 'medium' => 2, 'low' => 3, 'none' => 4, '' => 5];
        usort($items, static fn (array $a, array $b): int => [$rank[$a['severity']] ?? 5, $a['title']] <=> [$rank[$b['severity']] ?? 5, $b['title']]);
        return ['items' => $items, 'reachable' => $reachable];
    }

    /** @return array<string, mixed>|null the answer's data (an empty array when the item is unknown) */
    private function get(string $path): ?array
    {
        $file = $this->cacheDir !== '' ? rtrim($this->cacheDir, '/') . '/' . sha1($path) . '.json' : '';
        if ($file !== '' && is_file($file) && filemtime($file) > time() - self::TTL) {
            $cached = json_decode((string) file_get_contents($file), true);
            if (is_array($cached)) {
                return $cached;
            }
        }
        $body = ($this->fetch)(self::API . $path);
        if ($body === null) {
            return null;
        }
        $json = json_decode($body, true);
        if (!is_array($json)) {
            return null;
        }
        $data = is_array($json['data'] ?? null) ? $json['data'] : [];
        if ($file !== '') {
            @mkdir(dirname($file), 0o700, true);
            @file_put_contents($file, (string) json_encode($data), LOCK_EX);
        }
        return $data;
    }

    /**
     * Vulnerabilities in $data that affect $version (core answers are per
     * version already: $version null).
     *
     * @param array<string, mixed> $data
     * @return list<array{name: string, severity: string, score: string, fixed_in: string, unfixed: bool, cve: string, link: string}>
     */
    public static function affecting(array $data, ?string $version): array
    {
        $out = [];
        foreach (array_slice((array) ($data['vulnerability'] ?? []), 0, 500) as $v) {
            if (!is_array($v)) {
                continue;
            }
            $op = (array) ($v['operator'] ?? []);
            if ($version !== null && !self::matches($version, $op)) {
                continue;
            }
            $impact = (array) ($v['impact']['cvss'] ?? []);
            $cve = '';
            $link = '';
            foreach ((array) ($v['source'] ?? []) as $s) {
                if (!is_array($s)) {
                    continue;
                }
                if ($cve === '' && preg_match('/^CVE-\d{4}-\d{3,}$/', (string) ($s['id'] ?? ''))) {
                    $cve = (string) $s['id'];
                }
                if ($link === '' && preg_match('#^https://[^\s"<>]{1,300}$#', (string) ($s['link'] ?? ''))) {
                    $link = (string) $s['link'];
                }
            }
            $maxOp = (string) ($op['max_operator'] ?? '');
            $out[] = [
                'name' => mb_substr(strip_tags((string) ($v['name'] ?? 'Vulnerability')), 0, 300),
                'severity' => self::SEVERITY[(string) ($impact['severity'] ?? '')] ?? '',
                'score' => preg_match('/^\d{1,2}(\.\d)?$/', (string) ($impact['score'] ?? '')) ? (string) $impact['score'] : '',
                'fixed_in' => $maxOp === 'lt' && preg_match('/^[0-9A-Za-z.+-]{1,40}$/', (string) ($op['max_version'] ?? '')) ? (string) $op['max_version'] : '',
                'unfixed' => (string) ($op['unfixed'] ?? '0') === '1',
                'cve' => $cve,
                'link' => $link,
            ];
        }
        return $out;
    }

    /** @param array<string, mixed> $op */
    public static function matches(string $version, array $op): bool
    {
        $cmp = ['lt' => '<', 'le' => '<=', 'gt' => '>', 'ge' => '>=', 'eq' => '=='];
        foreach (['min', 'max'] as $side) {
            $o = (string) ($op[$side . '_operator'] ?? '');
            $v = (string) ($op[$side . '_version'] ?? '');
            if ($o === '' || $v === '') {
                continue;
            }
            if (!isset($cmp[$o]) || !version_compare($version, $v, $cmp[$o])) {
                return false;
            }
        }
        return true;
    }
}
