<?php

declare(strict_types=1);

namespace WhaCP\View;

use WhaCP\Security\Permissions as P;

/**
 * The website menu: one list for the sidebar, the section header and the
 * dashboard, so each shows the same pages to the same people.
 */
final class SiteMenu
{
    /** [key, label, icon, route, permission] in menu order. */
    private const ITEMS = [
        ['overview', 'Overview', 'speedometer2', 'sites.show', P::WEBSITES_VIEW],
        ['files', 'File manager', 'folder2-open', 'sites.files', P::FILES_MANAGE],
        ['disk', 'Disk usage', 'pie-chart', 'sites.disk', P::FILES_MANAGE],
        ['ftp', 'FTP / SFTP', 'hdd-network', 'sites.ftp', P::FTP_MANAGE],
        ['terminal', 'Terminal', 'terminal', 'sites.terminal', P::FILES_MANAGE],
        ['databases', 'Databases', 'database', 'sites.databases', P::DATABASES_VIEW],
        ['mail', 'Email', 'envelope', 'sites.mail', P::MAIL_MANAGE],
        ['dns', 'DNS', 'signpost', 'sites.dns', P::DNS_MANAGE],
        ['domains', 'Subdomains & addons', 'diagram-3', 'sites.domains', P::WEBSITES_MANAGE],
        ['ssl', 'SSL / HTTPS', 'shield-lock', 'sites.ssl', P::SSL_VIEW],
        ['backups', 'Backups', 'archive', 'sites.backups', P::BACKUPS_VIEW],
        ['apps', 'Applications', 'grid-3x3-gap', 'sites.apps', P::APPS_INSTALL],
        ['git', 'Git deployment', 'git', 'sites.git', P::DEPLOY_MANAGE],
        ['cron', 'Cron jobs', 'clock-history', 'sites.cron', P::CRON_VIEW],
        ['workers', 'Workers', 'gear-wide-connected', 'sites.workers', P::WORKERS_MANAGE],
        ['logs', 'Logs', 'journal-text', 'sites.logs', P::LOGS_VIEW],
        ['stats', 'Statistics', 'bar-chart-line', 'sites.stats', P::LOGS_VIEW],
        ['malware', 'Malware scan', 'bug', 'sites.malware', P::WEBSITES_VIEW],
        ['redis', 'Redis', 'lightning', 'sites.redis', P::WEBSITES_MANAGE],
        ['settings', 'PHP & settings', 'sliders', 'sites.edit', P::WEBSITES_MANAGE],
        ['advanced', 'Advanced', 'tools', 'sites.advanced', P::WEBSITES_MANAGE],
    ];

    /**
     * Menu entries the viewer may open.
     *
     * @param array<string, mixed> $site
     * @return list<array{key: string, label: string, icon: string, url: string}>
     */
    public static function items(View $view, array $site): array
    {
        $out = [];
        foreach (self::ITEMS as [$key, $label, $icon, $route, $perm]) {
            if ($view->hasRoute($route) && $view->can($perm) && $view->routeAllowed($route)) {
                $out[] = ['key' => $key, 'label' => $label, 'icon' => $icon, 'url' => $view->url($route, ['id' => (int) $site['id']])];
            }
        }
        return $out;
    }

    /** @param array<string, mixed> $site */
    public static function url(View $view, array $site, string $key): ?string
    {
        foreach (self::items($view, $site) as $i) {
            if ($i['key'] === $key) {
                return $i['url'];
            }
        }
        return null;
    }
}
