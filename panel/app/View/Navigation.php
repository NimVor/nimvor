<?php

declare(strict_types=1);

namespace WhaCP\View;

use WhaCP\Security\Permissions as P;

/**
 * The panel's areas for administrators and resellers: the sidebar, the
 * global search (Ctrl+K) and the dashboard's "Tools & settings" grid are
 * all built from this list, filtered by what the viewer may open.
 */
final class Navigation
{
    /**
     * group => list of [label, icon, route, permission, active prefix, description].
     *
     * @return array<string, list<array{0: string, 1: string, 2: string, 3: string, 4: string, 5: string}>>
     */
    public static function groups(): array
    {
        return [
            'Overview' => [
                ['Dashboard', 'speedometer2', 'dashboard', P::DASHBOARD_VIEW, '/', 'Server health, websites and recent activity'],
            ],
            'Hosting' => [
                ['Websites', 'globe2', 'websites', P::WEBSITES_VIEW, '/websites', 'Create and manage websites'],
                ['Domains', 'signpost-split', 'domains', P::WEBSITES_VIEW, '/domains', 'Domains, aliases and subdomains'],
                ['Databases', 'database', 'databases', P::DATABASES_VIEW, '/databases', 'MariaDB databases and users'],
                ['PHP', 'filetype-php', 'php', P::PHP_VIEW, '/php', 'PHP versions, extensions and php.ini'],
                ['WordPress', 'wordpress', 'wordpress', P::WEBSITES_VIEW, '/wordpress', 'WordPress Toolkit: updates, security, staging'],
                ['Cron Jobs', 'clock-history', 'cron', P::CRON_VIEW, '/cron', 'Scheduled tasks of the websites'],
                ['Workers', 'gear-wide-connected', 'workers', P::WORKERS_MANAGE, '/workers', 'Background processes kept running'],
                ['Redis', 'lightning', 'redis', P::REDIS_MANAGE, '/redis', 'Redis server and key browser'],
                ['Memcached', 'memory', 'memcached', P::REDIS_MANAGE, '/memcached', 'Memcached server'],
                ['DNS', 'signpost', 'dns', P::DNS_MANAGE, '/dns', 'DNS zones and records'],
                ['Email', 'envelope', 'mail', P::MAIL_MANAGE, '/mail', 'Mailboxes, aliases, spam filtering, webmail'],
                ['SSL', 'shield-lock', 'ssl', P::SSL_VIEW, '/ssl', 'Certificates and Let\'s Encrypt'],
            ],
            'Operations' => [
                ['Backups', 'archive', 'backups', P::BACKUPS_VIEW, '/backups', 'Backups, schedules and destinations'],
                ['Monitoring', 'activity', 'monitoring', P::MONITORING_VIEW, '/monitoring', 'Metrics history and alerts'],
                ['Notifications', 'bell', 'notifications', P::NOTIFICATIONS_VIEW, '/notifications', 'What happened and what failed'],
                ['Logs', 'journal-text', 'logs', P::LOGS_VIEW, '/logs', 'Web server, PHP and system logs'],
            ],
            'Security' => [
                ['Security', 'shield-check', 'security', P::SECURITY_VIEW, '/security', 'Firewall, SSH, Fail2ban, ModSecurity, malware'],
            ],
            'Server' => [
                ['Server', 'hdd-rack', 'server', P::SERVER_VIEW, '/server', 'Hostname, time, updates and reboot'],
                ['All servers', 'diagram-3', 'servers', P::SERVERS_MANAGE, '/servers', 'Other servers managed from here'],
                ['Services', 'cpu', 'services', P::SERVICES_VIEW, '/services', 'Start, stop and restart services'],
                ['IP addresses', 'ethernet', 'ips', P::SERVER_VIEW, '/ips', 'Addresses and their websites'],
                ['Resource limits', 'speedometer', 'limits', P::SERVER_VIEW, '/limits', 'CPU, memory and disk quotas per account'],
                ['Web terminal', 'terminal', 'terminal', P::SECURITY_MANAGE, '/terminal', 'A shell in the browser'],
                ['Packages', 'box-seam', 'packages', P::PACKAGES_MANAGE, '/packages', 'Install and update server software'],
                ['FFmpeg', 'film', 'ffmpeg', P::SERVER_VIEW, '/ffmpeg', 'Media conversion for websites'],
                ['FTP server', 'hdd-network', 'ftp', P::SERVER_MANAGE, '/ftp', 'FTP server and its settings'],
                ['System accounts', 'people', 'system_users', P::SYSTEM_USERS_VIEW, '/system-users', 'Linux accounts on the server'],
                ['Discovery', 'binoculars', 'discovery', P::WEBSITES_VIEW, '/discovery', 'Websites found on the server'],
            ],
            'Administration' => [
                ['Users & roles', 'person-gear', 'users', P::USERS_VIEW, '/users', 'Accounts, customers and roles'],
                ['Plans', 'card-checklist', 'plans', P::PLANS_MANAGE, '/plans', 'Hosting plans and their limits'],
                ['Settings', 'sliders', 'settings', P::SETTINGS_VIEW, '/settings', 'Panel settings'],
                ['Audit log', 'list-check', 'audit', P::AUDIT_VIEW, '/audit', 'Who changed what'],
                ['Plugins', 'plug', 'plugins', P::PLUGINS_MANAGE, '/admin/plugins', 'Panel plugins'],
            ],
        ];
    }

    /**
     * The areas a viewer may open, per group (routes that exist and that
     * the viewer's role and plan allow).
     *
     * @return array<string, list<array{label: string, icon: string, route: string, prefix: string, description: string, url: string}>>
     */
    public static function visible(View $view): array
    {
        $out = [];
        foreach (self::groups() as $group => $items) {
            foreach ($items as [$label, $icon, $route, $perm, $prefix, $desc]) {
                if (!$view->can($perm) || !$view->hasRoute($route) || !$view->routeAllowed($route)) {
                    continue;
                }
                $out[$group][] = ['label' => $label, 'icon' => $icon, 'route' => $route, 'prefix' => $prefix, 'description' => $desc, 'url' => $view->url($route)];
            }
        }
        return $out;
    }
}
