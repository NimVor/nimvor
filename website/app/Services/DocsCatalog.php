<?php

declare(strict_types=1);

namespace NimVorSite\Services;

/**
 * Which of the repository's Markdown files are the public documentation, where
 * each one lives on the site, and how the sidebar groups and names them.
 * Planning notes, audits and release reports are not published here.
 */
final class DocsCatalog
{
    /** The sidebar's groups, in order. */
    public const GROUPS = ['Start', 'Getting started', 'Using NimVor', 'How it works', 'Project', 'More'];

    /** @var array<string, array{string, string, int, string}> file => slug, group, order, label */
    private const ROOT = [
        'CONTRIBUTING.md' => ['contributing', 'Project', 30, 'Contributing'],
        'CODE_OF_CONDUCT.md' => ['code-of-conduct', 'Project', 31, 'Code of conduct'],
        'SECURITY.md' => ['security-policy', 'Project', 32, 'Security policy'],
        'CHANGELOG.md' => ['changelog', 'Project', 40, 'Changelog'],
    ];

    /** @var array<string, array{string, int, string}> docs slug => group, order, label */
    private const KNOWN = [
        'index' => ['Start', 0, 'Overview'],
        'installation' => ['Getting started', 10, 'Installation'],
        'troubleshooting' => ['Getting started', 20, 'Troubleshooting'],
        'administration' => ['Using NimVor', 10, 'Administration'],
        'api' => ['Using NimVor', 20, 'REST API'],
        'architecture' => ['How it works', 10, 'Architecture'],
        'architecture/package-management' => ['How it works', 11, 'Package management'],
        'agent' => ['How it works', 20, 'The agent'],
        'security' => ['How it works', 30, 'Security'],
        'security/installer-review' => ['How it works', 31, 'Installer review'],
        'plugins' => ['How it works', 40, 'Plugins'],
        'development' => ['Project', 10, 'Development'],
        'development/roadmap' => ['Project', 11, 'Roadmap'],
    ];

    private const EXCLUDED = ['docs/release/', 'docs/development/app-installer-plan.md', 'docs/development/feature-expansion-audit.md', 'docs/security/ga-assessment.md', 'docs/migration/'];

    /** @return array{slug: string, group: string, order: int, label: string}|null null when the file is not published */
    public static function entry(string $path): ?array
    {
        if (isset(self::ROOT[$path])) {
            [$slug, $group, $order, $label] = self::ROOT[$path];
            return ['slug' => $slug, 'group' => $group, 'order' => $order, 'label' => $label];
        }
        if (!str_starts_with($path, 'docs/') || !str_ends_with($path, '.md') || str_contains($path, '..')) {
            return null;
        }
        foreach (self::EXCLUDED as $x) {
            if ($path === $x || (str_ends_with($x, '/') && str_starts_with($path, $x))) {
                return null;
            }
        }
        $rel = substr($path, 5, -3);
        $slug = strtolower($rel === 'README' ? 'index' : (preg_replace('#/README$#i', '', $rel) ?? $rel));
        if (preg_match('#^[a-z0-9][a-z0-9._-]*(/[a-z0-9][a-z0-9._-]*)*$#', $slug) !== 1) {
            return null;
        }
        [$group, $order, $label] = self::KNOWN[$slug] ?? ['More', 900, ucfirst(str_replace(['-', '_'], ' ', basename($slug)))];
        return ['slug' => $slug, 'group' => $group, 'order' => $order, 'label' => $label];
    }

    /** The page's address on the site. */
    public static function url(string $slug): string
    {
        return $slug === 'index' ? '/docs' : '/docs/' . $slug;
    }

    /**
     * Where a link found in the file at $from (a repository path) should go: the page
     * on this site when it is published here, otherwise the file on GitHub.
     *
     * @param array<string, string> $slugs source path => slug of the published pages
     */
    public static function resolve(string $from, string $target, array $slugs, string $repoUrl, string $branch): string
    {
        $target = trim($target);
        if ($target === '' || $target[0] === '#' || preg_match('#^[a-z][a-z0-9+.-]*:#i', $target) === 1) {
            return $target;
        }
        if (str_starts_with($target, '//')) {
            return '';
        }
        $fragment = '';
        if (($pos = strpos($target, '#')) !== false) {
            $fragment = substr($target, $pos);
            $target = substr($target, 0, $pos);
        }
        $query = strpos($target, '?');
        if ($query !== false) {
            $target = substr($target, 0, $query);
        }
        $parts = $target === '' ? [] : explode('/', ltrim($target, '/'));
        $stack = str_starts_with($target, '/') ? [] : array_values(array_filter(explode('/', dirname($from)), static fn (string $s): bool => $s !== '' && $s !== '.'));
        foreach ($parts as $p) {
            if ($p === '..') {
                array_pop($stack);
            } elseif ($p !== '.' && $p !== '') {
                $stack[] = $p;
            }
        }
        $path = implode('/', $stack);
        foreach ([$path, $path . '/README.md'] as $candidate) {
            if (isset($slugs[$candidate])) {
                return self::url($slugs[$candidate]) . $fragment;
            }
        }
        $isDir = str_ends_with($target, '/') || !str_contains(basename($path), '.');
        return $repoUrl . '/' . ($isDir ? 'tree' : 'blob') . '/' . rawurlencode($branch) . '/' . implode('/', array_map('rawurlencode', $stack)) . $fragment;
    }
}
