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
                ['Databases', 'database', 'databases', P::DATABASES_VIEW, '/databases', 'MariaDB databases and users', [
                    ['Databases and users', 'databases', P::DATABASES_VIEW],
                    ['Database server', 'databases.server', P::SERVER_VIEW],
                    ['Database server settings', 'databases.server.settings_page', P::SERVER_VIEW],
                    ['Database connections', 'databases.server.connections', P::SERVER_VIEW],
                    ['Database logs', 'databases.server.logs', P::LOGS_VIEW],
                ]],
                ['PHP', 'filetype-php', 'php', P::PHP_VIEW, '/php', 'PHP versions, extensions and php.ini'],
                ['WordPress', 'wordpress', 'wordpress', P::WEBSITES_VIEW, '/wordpress', 'WordPress Toolkit: updates, security, staging'],
                ['Cron Jobs', 'clock-history', 'cron', P::CRON_VIEW, '/cron', 'Scheduled tasks of the websites'],
                ['Workers', 'gear-wide-connected', 'workers', P::WORKERS_MANAGE, '/workers', 'Background processes kept running'],
                ['Redis', 'lightning', 'redis', P::REDIS_MANAGE, '/redis', 'Redis server and key browser'],
                ['Memcached', 'memory', 'memcached', P::REDIS_MANAGE, '/memcached', 'Memcached server'],
                ['DNS', 'signpost', 'dns', P::DNS_MANAGE, '/dns', 'DNS zones and records'],
                ['Email', 'envelope', 'mail', P::MAIL_MANAGE, '/mail', 'Mailboxes, aliases, spam filtering, webmail', [
                    ['Mail server', 'mail.admin.server', P::SERVER_VIEW],
                    ['Mailboxes and aliases', 'mail', P::MAIL_MANAGE, [], ['/mail/domains']],
                    ['Mail queue', 'mail.admin.queue', P::SERVER_VIEW],
                    ['Delivery reports', 'mail.admin.reports', P::SERVER_VIEW],
                    ['Statistics', 'mail.admin.reports', P::SERVER_VIEW, ['view' => 'stats']],
                    ['Greylisting', 'mail.greylist', P::SERVER_VIEW],
                    ['Greylisting report', 'mail.greylist.report', P::SERVER_VIEW],
                    ['Tools', 'mail.admin.tools', P::SERVER_VIEW],
                ]],
                ['SSL', 'shield-lock', 'ssl', P::SSL_VIEW, '/ssl', 'Certificates and Let\'s Encrypt'],
            ],
            'Operations' => [
                ['Backups', 'archive', 'backups', P::BACKUPS_VIEW, '/backups', 'Backups, schedules and destinations'],
                ['Monitoring', 'activity', 'monitoring', P::MONITORING_VIEW, '/monitoring', 'Metrics history and alerts'],
                ['Notifications', 'bell', 'notifications', P::NOTIFICATIONS_VIEW, '/notifications', 'What happened and what failed'],
                ['Logs', 'journal-text', 'logs', P::LOGS_VIEW, '/logs', 'Web server, PHP and system logs'],
            ],
            'Security' => [
                ['Security', 'shield-check', 'security', P::SECURITY_VIEW, '/security', 'Firewall, SSH, Fail2ban, ModSecurity, malware', self::securityChildren()],
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
     * Security's pages, in the order of the sidebar.
     *
     * @return list<array<int, mixed>>
     */
    public static function securityChildren(): array
    {
        return [
            ['Overview', 'security', P::SECURITY_VIEW],
            ['Firewall', 'security.firewall', P::SECURITY_VIEW],
            ['SSH', 'security.ssh', P::SECURITY_VIEW],
            ['Brute-force protection', 'security.fail2ban', P::SECURITY_VIEW],
            ['Malware scan', 'security.malware', P::SECURITY_VIEW],
            ['Shell access', 'security.shell', P::SECURITY_VIEW],
            ['ModSecurity', 'security.modsec', P::SECURITY_VIEW],
            ['Configure Security Policies', 'security.policies', P::SECURITY_VIEW],
            ['Compiler Access', 'security.compilers', P::SECURITY_VIEW],
            ['SMTP Restrictions', 'security.smtp', P::SECURITY_VIEW],
            ['cPHulk Brute Force Protection', 'security.hulk', P::SECURITY_VIEW],
            ['Password Strength Configuration', 'security.password_strength', P::SECURITY_VIEW],
            ['Shell Fork Bomb Protection', 'security.forkbomb', P::SECURITY_VIEW],
            ['Traceroute Enable/Disable', 'security.traceroute', P::SECURITY_VIEW],
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
            foreach ($items as $item) {
                [$label, $icon, $route, $perm, $prefix, $desc] = $item;
                if (!$view->can($perm) || !$view->hasRoute($route) || !$view->routeAllowed($route)) {
                    continue;
                }
                $children = [];
                foreach ($item[6] ?? [] as $c) {
                    [$cLabel, $cRoute, $cPerm] = $c;
                    $query = $c[3] ?? [];
                    if (!$view->can($cPerm) || !$view->hasRoute($cRoute) || !$view->routeAllowed($cRoute)) {
                        continue;
                    }
                    $path = $view->url($cRoute);
                    $children[] = ['label' => $cLabel, 'url' => $view->url($cRoute, [], $query), 'active' => self::childActive($view, $path, $query, $c[4] ?? [])];
                }
                // One child only (e.g. a reseller's Email): the parent is enough.
                if (count($children) < 2) {
                    $children = [];
                }
                $out[$group][] = ['label' => $label, 'icon' => $icon, 'route' => $route, 'prefix' => $prefix, 'description' => $desc, 'url' => $view->url($route), 'children' => $children];
            }
        }
        return $out;
    }

    /**
     * @param array<string, string> $query
     * @param list<string> $also paths below which the child is active too
     */
    private static function childActive(View $view, string $path, array $query, array $also): bool
    {
        $current = (string) ($view->shared('current_path') ?? '');
        $currentView = (string) ($view->shared('current_view') ?? '');
        if ($current === $path && ($query['view'] ?? '') === $currentView) {
            return true;
        }
        foreach ($also as $prefix) {
            if (str_starts_with($current, rtrim($prefix, '/') . '/')) {
                return true;
            }
        }
        return false;
    }
}
