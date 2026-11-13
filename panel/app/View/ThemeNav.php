<?php

declare(strict_types=1);

namespace NimVor\View;

/**
 * Arranges one website's tools for the themes that group them (Classic, Compact, Simple).
 *
 * The tools themselves come from SiteMenu, which already applies the viewer's permissions and plan. A tool that no
 * category names lands in Advanced, so a theme can regroup the tools but never hide one.
 */
final class ThemeNav
{
    /** category => [label, icon] in display order */
    public const CATEGORIES = [
        'files' => ['Files', 'folder2-open'],
        'databases' => ['Databases', 'database'],
        'domains' => ['Domains', 'globe2'],
        'email' => ['Email', 'envelope'],
        'security' => ['Security', 'shield-check'],
        'software' => ['Software', 'grid-3x3-gap'],
        'advanced' => ['Advanced', 'tools'],
    ];

    /** SiteMenu key => category; anything missing is Advanced. */
    private const MAP = [
        'files' => 'files', 'disk' => 'files', 'ftp' => 'files', 'backups' => 'files',
        'databases' => 'databases', 'mongo' => 'databases', 'redis' => 'databases', 'memcached' => 'databases',
        'dns' => 'domains', 'subdomains' => 'domains', 'addons' => 'domains', 'redirects' => 'domains', 'cloudflare' => 'domains',
        'mail' => 'email',
        'ssl' => 'security', 'security' => 'security', 'malware' => 'security', 'firewall' => 'security', 'access' => 'security', 'traffic' => 'security', 'accesslists' => 'security', 'headers' => 'security',
        'apps' => 'software', 'wordpress' => 'software', 'php' => 'software', 'nodejs' => 'software', 'python' => 'software', 'git' => 'software',
    ];

    public static function category(string $key): string
    {
        return self::MAP[$key] ?? 'advanced';
    }

    /**
     * The tools by category, in display order; the Overview page is not a tool and is left out.
     *
     * @param list<array{key: string, label: string, icon: string, url: string}> $items from SiteMenu::items()
     * @return array<string, array{label: string, icon: string, items: list<array{key: string, label: string, icon: string, url: string}>}>
     */
    public static function grouped(array $items): array
    {
        $out = [];
        foreach (self::CATEGORIES as $cat => [$label, $icon]) {
            $out[$cat] = ['label' => $label, 'icon' => $icon, 'items' => []];
        }
        foreach ($items as $item) {
            if ($item['key'] === 'overview') {
                continue;
            }
            $out[self::category($item['key'])]['items'][] = $item;
        }
        return array_filter($out, static fn (array $g): bool => $g['items'] !== []);
    }

    /**
     * The tools with these keys, in the order given (for shortcuts); the ones the viewer may not open are skipped.
     *
     * @param list<array{key: string, label: string, icon: string, url: string}> $items
     * @param list<string> $keys
     * @return list<array{key: string, label: string, icon: string, url: string}>
     */
    public static function pick(array $items, array $keys): array
    {
        $byKey = [];
        foreach ($items as $item) {
            $byKey[$item['key']] = $item;
        }
        $out = [];
        foreach ($keys as $key) {
            if (isset($byKey[$key])) {
                $out[] = $byKey[$key];
            }
        }
        return $out;
    }

    /**
     * Account-level pages the viewer may open (notifications, backups overview, …), without the Websites entry,
     * which every theme shows itself. Built from the same list as the administrators' sidebar.
     *
     * @return list<array{label: string, icon: string, url: string}>
     */
    public static function accountPages(View $view): array
    {
        $out = [];
        foreach (Navigation::visible($view) as $items) {
            foreach ($items as $item) {
                if (in_array($item['route'], ['dashboard', 'websites'], true)) {
                    continue;
                }
                $out[] = ['label' => $item['label'], 'icon' => $item['icon'], 'url' => $item['url']];
            }
        }
        return $out;
    }
}
