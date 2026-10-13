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
        ['mongo', 'MongoDB', 'database-fill-gear', 'sites.mongo', P::WEBSITES_MANAGE],
        ['mail', 'Email', 'envelope', 'sites.mail', P::MAIL_MANAGE],
        ['dns', 'DNS', 'signpost', 'sites.dns', P::DNS_MANAGE],
        ['subdomains', 'Subdomains', 'diagram-3', 'sites.subdomains', P::WEBSITES_MANAGE],
        ['addons', 'Addon domains', 'plus-square', 'sites.addons', P::WEBSITES_MANAGE],
        ['ssl', 'SSL / HTTPS', 'shield-lock', 'sites.ssl', P::SSL_VIEW],
        ['cloudflare', 'Cloudflare', 'cloud', 'sites.cloudflare', P::WEBSITES_MANAGE],
        ['backups', 'Backups', 'archive', 'sites.backups', P::BACKUPS_VIEW],
        ['apps', 'Applications', 'grid-3x3-gap', 'sites.apps', P::APPS_INSTALL],
        ['wordpress', 'WordPress', 'wordpress', 'sites.wordpress', P::WEBSITES_VIEW],
        ['git', 'Git deployment', 'git', 'sites.git', P::DEPLOY_MANAGE],
        ['cron', 'Cron jobs', 'clock-history', 'sites.cron', P::CRON_VIEW],
        ['workers', 'Workers', 'gear-wide-connected', 'sites.workers', P::WORKERS_MANAGE],
        ['logs', 'Logs', 'journal-text', 'sites.logs', P::LOGS_VIEW],
        ['stats', 'Statistics', 'bar-chart-line', 'sites.stats', P::LOGS_VIEW],
        ['health', 'Site health', 'heart-pulse', 'sites.health', P::WEBSITES_VIEW],
        ['security', 'Security', 'shield-check', 'sites.security', P::WEBSITES_VIEW],
        ['malware', 'Malware scan', 'bug', 'sites.malware', P::WEBSITES_VIEW],
        ['firewall', 'Web firewall', 'shield-shaded', 'sites.firewall', P::WEBSITES_VIEW],
        ['redis', 'Redis', 'lightning', 'sites.redis', P::WEBSITES_MANAGE],
        ['memcached', 'Memcached', 'hdd-stack', 'sites.memcached', P::WEBSITES_VIEW],
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
