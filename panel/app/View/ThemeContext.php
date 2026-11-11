<?php

declare(strict_types=1);

namespace NimVor\View;

/**
 * What every theme's frame needs to know, worked out once: the account's websites, which one the page is about, and
 * that website's tools. The themes arrange it differently; none of them computes it on its own.
 */
final class ThemeContext
{
    /**
     * @return array{
     *     account: array{sites: list<array<string, mixed>>, plan: string, usage: list<array<string, mixed>>},
     *     sites: list<array<string, mixed>>,
     *     site: array<string, mixed>|null,
     *     items: list<array{key: string, label: string, icon: string, url: string}>,
     *     accountPages: list<array{label: string, icon: string, url: string}>,
     *     entries: list<array{label: string, icon: string, url: string}>,
     *     menu: array<string, mixed>|null
     * }
     */
    public static function build(View $view): array
    {
        $account = $view->themeAccount();
        $menu = $view->shared('site_menu');
        $sites = $account['sites'];
        $site = null;
        if ($menu !== null) {
            $site = $menu['site'];
        } else {
            // The home page shows one website at a time: the one asked for (?site=), else the first.
            $wanted = (int) (($view->shared('current_query') ?? [])['site'] ?? 0);
            foreach ($sites as $s) {
                if ($wanted > 0 && (int) $s['id'] === $wanted) {
                    $site = $s;
                }
            }
            $site ??= $sites[0] ?? null;
        }
        $items = $menu !== null ? $menu['items'] : ($site !== null ? SiteMenu::items($view, $site) : []);
        $accountPages = ThemeNav::accountPages($view);
        $entries = [];
        foreach ($items as $i) {
            $entries[] = ['label' => $i['label'], 'icon' => $i['icon'], 'url' => $i['url']];
        }
        foreach ($accountPages as $p) {
            $entries[] = $p;
        }
        $entries[] = ['label' => 'Appearance', 'icon' => 'palette', 'url' => $view->url('appearance')];
        return ['account' => $account, 'sites' => $sites, 'site' => $site, 'items' => $items, 'accountPages' => $accountPages, 'entries' => $entries, 'menu' => $menu];
    }
}
