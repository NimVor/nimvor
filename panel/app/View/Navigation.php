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
                    ['Sending limits', 'mail.limits', P::SERVER_VIEW],
                    ['Tools', 'mail.admin.tools', P::SERVER_VIEW],
                ]],
                ['SSL', 'shield-lock', 'ssl', P::SSL_VIEW, '/ssl', 'Certificates and Let\'s Encrypt', [
                    ['SSL hosts', 'ssl.hosts', P::SSL_VIEW],
                    ['AutoSSL', 'ssl.autossl', P::SSL_VIEW],
                    ['Certificates', 'ssl.certificates', P::SSL_VIEW],
                    ['Keys and CSRs', 'ssl.keys', P::SSL_VIEW],
                    ['TLS configuration', 'ssl.tls', P::SSL_VIEW],
                    ['Services SSL', 'ssl.services', P::SSL_VIEW],
                ]],
            ],
            'Operations' => [
                ['Backups', 'archive', 'backups', P::BACKUPS_VIEW, '/backups', 'Backups, schedules and destinations'],
                ['Monitoring', 'activity', 'monitoring', P::MONITORING_VIEW, '/monitoring', 'Metrics history, live statistics and processes', [
                    ['History and alerts', 'monitoring', P::MONITORING_VIEW],
                    ['Live', 'monitoring.live', P::MONITORING_VIEW],
                    ['Processes', 'monitoring.processes', P::MONITORING_VIEW, [], ['/monitoring/processes']],
                ]],
                ['Notifications', 'bell', 'notifications', P::NOTIFICATIONS_VIEW, '/notifications', 'What happened and what failed'],
                ['Logs', 'journal-text', 'logs', P::LOGS_VIEW, '/logs', 'Web server, PHP, mail, security and WhaCP logs', [
                    ['All server logs', 'logs', P::LOGS_VIEW],
                    ['Web server', 'logs', P::LOGS_VIEW, ['view' => 'web']],
                    ['PHP-FPM', 'logs', P::LOGS_VIEW, ['view' => 'php']],
                    ['Mail server', 'logs', P::LOGS_VIEW, ['view' => 'mail']],
                    ['Database server', 'logs', P::LOGS_VIEW, ['view' => 'database']],
                    ['Security', 'logs', P::LOGS_VIEW, ['view' => 'security']],
                    ['SSL / Let\'s Encrypt', 'logs', P::LOGS_VIEW, ['view' => 'ssl']],
                    ['System', 'logs', P::LOGS_VIEW, ['view' => 'system']],
                    ['Services', 'logs', P::LOGS_VIEW, ['view' => 'services']],
                    ['WhaCP', 'logs', P::LOGS_VIEW, ['view' => 'whacp']],
                    ['Audit log', 'audit', P::AUDIT_VIEW],
                    ['AutoSSL log', 'ssl.autossl', P::SSL_VIEW],
                    ['ModSecurity events', 'security.modsec', P::SECURITY_VIEW, ['tab' => 'events']],
                    ['Mail delivery reports', 'mail.admin.reports', P::SERVER_VIEW],
                    ['Failed logins', 'security.hulk', P::SECURITY_VIEW, ['tab' => 'report']],
                ]],
            ],
            'Security' => [
                ['Security', 'shield-check', 'security', P::SECURITY_VIEW, '/security', 'Firewall, SSH, Fail2ban, ModSecurity, malware', self::securityChildren()],
            ],
            'Server' => [
                ['Server', 'hdd-rack', 'server', P::SERVER_VIEW, '/server', 'Hostname, time, updates and reboot'],
                ['Health', 'heart-pulse', 'server.health', P::SERVER_VIEW, '/server/health', 'Is anything wrong? Every check in one place'],
                ['Setup wizard', 'magic', 'setup', P::SERVER_MANAGE, '/setup', 'Server profile, what is done and what is left'],
                ['Components', 'puzzle', 'components', P::SERVER_VIEW, '/components', 'Web, PHP, email, DNS, FTP, caches, security: what each needs'],
                ['PHP Manager', 'filetype-php', 'php.manager', P::PACKAGES_MANAGE, '/php/manager', 'Choose the PHP versions and extensions, review, apply'],
                ['Runtimes', 'hexagon', 'runtimes', P::SERVER_VIEW, '/runtimes', 'Node.js and Python versions for website workers'],
                ['MongoDB', 'database', 'mongo', P::MONGO_VIEW, '/mongo', 'MongoDB databases and users'],
                ['Cloudflare', 'cloud', 'cloudflare', P::SERVER_VIEW, '/cloudflare', 'CDN and DNS through your Cloudflare account'],
                ['Nginx', 'hdd-network', 'nginx', P::SERVER_VIEW, '/nginx', 'Web server state, config test and tuning'],
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
                ['Users', 'people', 'users', P::USERS_VIEW, '/users', 'Accounts, customers and resellers'],
                ['Roles', 'person-badge', 'roles', P::USERS_VIEW, '/roles', 'What each kind of user may do'],
                ['Plans', 'card-checklist', 'plans', P::PLANS_MANAGE, '/plans', 'Hosting plans and their limits'],
                ['Bandwidth Limits', 'speedometer2', 'accounts.bandwidth', P::USERS_MANAGE, '/accounts/bandwidth', 'Monthly traffic per account, exceeders and resets'],
                ['Basic WebHost Settings', 'house-gear', 'settings.basic', P::SETTINGS_VIEW, '/settings/basic', 'Contact, panel address, name servers, defaults for new accounts'],
                ['Tweak Settings', 'sliders2', 'settings.tweak', P::SETTINGS_VIEW, '/settings/tweak', 'Every setting with its default: search, change, reset'],
                ['Settings', 'sliders', 'settings', P::SETTINGS_VIEW, '/settings', 'Panel settings'],
                ['Application Manager', 'grid-3x3-gap', 'apps.manager', P::SETTINGS_VIEW, '/apps-manager', 'Installer categories and applications, and what customers can install'],
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
            ['File integrity', 'security.integrity', P::SECURITY_VIEW],
            ['TLS audit', 'security.tls', P::SECURITY_VIEW],
            ['Vulnerability advisories', 'security.advisories', P::SECURITY_VIEW],
            ['Shell access', 'security.shell', P::SECURITY_VIEW],
            ['ModSecurity', 'security.modsec', P::SECURITY_VIEW],
            ['Configure Security Policies', 'security.policies', P::SECURITY_VIEW],
            ['Compiler Access', 'security.compilers', P::SECURITY_VIEW],
            ['SMTP Restrictions', 'security.smtp', P::SECURITY_VIEW],
            ['Outgoing Connections', 'security.egress', P::SECURITY_VIEW],
            ['cPHulk Brute Force Protection', 'security.hulk', P::SECURITY_VIEW],
            ['Password Strength Configuration', 'security.password_strength', P::SECURITY_VIEW],
            ['Shell Fork Bomb Protection', 'security.forkbomb', P::SECURITY_VIEW],
            ['Traceroute Enable/Disable', 'security.traceroute', P::SECURITY_VIEW],
        ];
    }

    /** Icon and description of each Security page (Tools & settings cards). */
    /** SSL/TLS's pages as dashboard cards: route => [icon, description]. */
    public const SSL_CARDS = [
        'ssl.hosts' => ['shield-lock', 'Every host name and the certificate that answers'],
        'ssl.autossl' => ['arrow-repeat', 'Let\'s Encrypt for every website, renewals and logs'],
        'ssl.certificates' => ['patch-check', 'Every certificate on the server'],
        'ssl.keys' => ['key', 'Private keys and signing requests'],
        'ssl.tls' => ['sliders', 'Protocols and ciphers of the websites'],
        'ssl.services' => ['hdd-network', 'Panel, mail server and host name certificates'],
    ];

    public const SECURITY_CARDS = [
        'security' => ['shield-check', 'Safety rating and one-click fixes'],
        'security.firewall' => ['bricks', 'Open ports and allowed addresses'],
        'security.ssh' => ['key', 'SSH settings and administrator keys'],
        'security.fail2ban' => ['person-x', 'Bans addresses that guess passwords'],
        'security.malware' => ['bug', 'Scan websites for malware'],
        'security.shell' => ['terminal', 'Who may use a shell'],
        'security.modsec' => ['shield-shaded', 'Web application firewall'],
        'security.policies' => ['clipboard-check', 'Sign-in and password policies'],
        'security.compilers' => ['code-square', 'Who may run compilers'],
        'security.smtp' => ['envelope-x', 'Only the mail server sends mail'],
        'security.egress' => ['box-arrow-up-right', 'Websites\' outgoing connections'],
        'security.hulk' => ['shield-lock', 'Blocks panel and login attacks'],
        'security.password_strength' => ['input-cursor-text', 'Required password strength'],
        'security.forkbomb' => ['diagram-2', 'Process limits for accounts'],
        'security.traceroute' => ['signpost-split', 'Who may run traceroute'],
    ];

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
                    $children[] = ['label' => $cLabel, 'route' => $cRoute, 'url' => $view->url($cRoute, [], $query), 'active' => self::childActive($view, $path, $query, $c[4] ?? [])];
                }
                // SSL/TLS's, Monitoring's and Logs' sub-pages are for server administrators.
                if (in_array($route, ['ssl', 'monitoring', 'logs'], true) && !$view->serverScope()) {
                    $children = [];
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
     * What each permission opens, by menu page: every route a permission
     * guards is matched to the deepest menu entry above its path. Built
     * from the routes themselves, so the role editor stays complete when
     * pages are added.
     *
     * @return array<string, list<string>> permission => page labels
     */
    public static function coverage(\WhaCP\Http\Router $router): array
    {
        $entries = [];
        $top = [];
        foreach (self::groups() as $items) {
            foreach ($items as $item) {
                $top[rtrim($item[4], '/')] = true;
                $entries[] = [rtrim($item[4], '/'), $item[0]];
            }
        }
        foreach (self::groups() as $items) {
            foreach ($items as $item) {
                [$label, , , , $prefix] = $item;
                foreach ($item[6] ?? [] as $c) {
                    if (($c[3] ?? []) !== [] || !$router->has($c[1])) {
                        continue;
                    }
                    try {
                        $path = rtrim($router->url($c[1]), '/');
                    } catch (\InvalidArgumentException) {
                        continue;
                    }
                    if (!isset($top[$path])) {
                        $entries[] = [$path, $c[0] === $label ? $label : $label . ' › ' . $c[0]];
                    }
                }
            }
        }
        $site = [
            '/' => 'Dashboard', '/sites' => 'Website pages (files, PHP, SSL, logs… of a website)',
            '/api' => 'API (the same actions with a token)', '/topbar' => 'Top bar server status',
            '/whacp' => 'WhaCP update',
        ];
        $out = [];
        foreach ($router->routes() as $r) {
            if ($r->permission === null) {
                continue;
            }
            $best = '';
            $bestLen = -1;
            foreach ($entries as [$path, $label]) {
                if ($path !== '' && ($r->pattern === $path || str_starts_with($r->pattern, $path . '/')) && strlen($path) > $bestLen) {
                    [$best, $bestLen] = [$label, strlen($path)];
                }
            }
            if ($best === '') {
                $first = '/' . explode('/', ltrim($r->pattern, '/'))[0];
                $best = $site[$first] ?? ucfirst(str_replace(['-', '_'], ' ', ltrim($first, '/')));
            }
            $out[$r->permission][$best] = true;
        }
        return array_map(static function (array $labels): array {
            $l = array_keys($labels);
            sort($l, SORT_NATURAL | SORT_FLAG_CASE);
            return $l;
        }, $out);
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
