<?php

declare(strict_types=1);

namespace NimVor\View;

/**
 * What an account should look at, worked out from data the home page already has (the websites and their backup
 * counts). The themes show it in their own way: a list of items needing attention, a friendly checklist, or a strip.
 */
final class ThemeInsights
{
    /**
     * @param list<array<string, mixed>> $sites
     * @param array<int, int> $backups completed backups per website id
     * @return list<array{site: string, id: int, level: string, icon: string, text: string, action: string, url: string}>
     */
    public static function attention(View $view, array $sites, array $backups): array
    {
        $out = [];
        foreach ($sites as $s) {
            $id = (int) $s['id'];
            $domain = (string) $s['domain'];
            $url = static fn (string $key): string => SiteMenu::url($view, $s, $key) ?? $view->url('sites.show', ['id' => $id]);
            if (!empty($s['account_suspended'])) {
                $out[] = ['site' => $domain, 'id' => $id, 'level' => 'bad', 'icon' => 'slash-circle', 'text' => 'The account is suspended', 'action' => 'Open', 'url' => $view->url('sites.show', ['id' => $id])];
                continue;
            }
            if (($s['status'] ?? '') === 'error') {
                $out[] = ['site' => $domain, 'id' => $id, 'level' => 'bad', 'icon' => 'exclamation-triangle', 'text' => 'The last change failed', 'action' => 'Review', 'url' => $view->url('sites.show', ['id' => $id])];
            }
            if (empty($s['enabled'])) {
                continue;
            }
            if (empty($s['https']) && SiteMenu::url($view, $s, 'ssl') !== null) {
                $out[] = ['site' => $domain, 'id' => $id, 'level' => 'warn', 'icon' => 'unlock', 'text' => 'Not secured with HTTPS', 'action' => 'Secure it', 'url' => $url('ssl')];
            }
            if ((int) ($backups[$id] ?? 0) === 0 && SiteMenu::url($view, $s, 'backups') !== null) {
                $out[] = ['site' => $domain, 'id' => $id, 'level' => 'warn', 'icon' => 'archive', 'text' => 'No backup yet', 'action' => 'Back up', 'url' => $url('backups')];
            }
        }
        return $out;
    }
}
