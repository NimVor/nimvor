<?php

declare(strict_types=1);

namespace NimVor\Dashboard;

use NimVor\Security\Permissions as P;

/**
 * Every widget the dashboard can show, in one place: what it is called, where
 * it belongs, who may see it, how big it starts, how often it refreshes and
 * how long its measurement is kept. A widget is added by listing it here and
 * giving it a data method in DashboardWidgetsService and a view in
 * resources/views/dashboard/widgets/.
 */
final class WidgetRegistry
{
    /** The categories a widget is filed under (the widget library groups by these). */
    public const CATEGORIES = ['Overview', 'Server', 'Websites', 'Security', 'Databases', 'Performance', 'Backups', 'DNS & Email', 'DevOps', 'System Health'];

    /** @var array<string, Widget>|null */
    private ?array $widgets = null;

    /** @return array<string, Widget> */
    public function all(): array
    {
        return $this->widgets ??= $this->define();
    }

    public function find(string $id): ?Widget
    {
        return $this->all()[$id] ?? null;
    }

    /**
     * The widgets a viewer may see.
     *
     * @param callable(string): bool $can
     * @return array<string, Widget>
     */
    public function visibleTo(callable $can): array
    {
        return array_filter($this->all(), static fn (Widget $w): bool => $can($w->permission));
    }

    /** @return array<string, Widget> */
    private function define(): array
    {
        // The default order fills whole rows of the 12-column grid: eight wide + four narrow, every row full.
        $list = [
            // The first things on the dashboard: four live tiles (width 3 each) and three cards (width 4 each), as before, now
            // widgets like the rest: they can be hidden, moved and resized. The tiles and the services list are filled by
            // the live poll (public/assets/js/dashboard.js), so they cost nothing to draw.
            new Widget('tile_cpu', 'CPU', 'cpu', 'Performance', P::DASHBOARD_VIEW, 'sm', 0, 3600, true, true, 'Live CPU use with a sparkline of the last hour.', '', '', true),
            new Widget('tile_memory', 'Memory', 'memory', 'Performance', P::DASHBOARD_VIEW, 'sm', 0, 3600, true, true, 'Live memory use with a sparkline of the last hour.', '', '', true),
            new Widget('tile_disk', 'Disk (/)', 'hdd', 'System Health', P::DASHBOARD_VIEW, 'sm', 0, 3600, true, true, 'Live use of the main filesystem with a sparkline of the last hour.', '', '', true),
            new Widget('tile_load', 'Load average', 'speedometer', 'Performance', P::DASHBOARD_VIEW, 'sm', 0, 3600, true, true, 'Live load average (1, 5 and 15 minutes) with a sparkline of the last hour.', '', '', true),
            new Widget('software', 'Software', 'box-seam', 'Server', P::SERVER_VIEW, 'md', 60, 30, true, true, 'The parts of the server (web server, PHP, databases, caches, mail, FTP, security) as tiles, each with start, stop, restart and its settings page.', 'components', 'Components'),
            new Widget('websites_summary', 'Websites', 'globe2', 'Websites', P::DASHBOARD_VIEW, 'md', 0, 60, true, true, 'How many websites exist, are enabled and use HTTPS, the PHP versions in use and certificates close to expiry.', 'websites', 'View all'),
            new Widget('alert_center', 'Alert center', 'bell', 'Overview', P::MONITORING_VIEW, 'lg', 30, 15, false, true, 'Open alerts by severity with acknowledge and resolve, and what was resolved in the last day.', 'monitoring', 'Monitoring'),
            new Widget('attention', 'Needs attention', 'heart-pulse', 'Overview', P::WEBSITES_VIEW, 'md', 0, 30, true, true, 'Websites with something to fix: mail records, certificates, backups.', 'sites.healthcheck', 'Site health'),
            new Widget('performance_history', 'Performance history', 'activity', 'Performance', P::MONITORING_VIEW, 'lg', 60, 30, false, true, 'CPU, memory, load and network over the last hour, 6 hours, 24 hours or 7 days, drawn from what the monitor recorded.', 'monitoring', 'Monitoring'),
            new Widget('disk', 'Disk', 'hdd', 'System Health', P::SERVER_VIEW, 'md', 0, 60, true, true, 'Space on each filesystem and where it goes.', 'server.disk', 'Disk usage'),
            new Widget('website_health', 'Website health', 'activity', 'Websites', P::WEBSITES_VIEW, 'lg', 60, 30, false, true, 'Real HTTP checks of every website: up, failing, slow, uptime over 24 hours.', 'websites', 'Websites'),
            new Widget('website_overview', 'Website statistics', 'bar-chart', 'Websites', P::WEBSITES_VIEW, 'md', 0, 60, true, true, 'Active, disabled and suspended websites, HTTPS use and how many were created each month.', 'websites', 'Websites'),
            new Widget('security_overview', 'Security overview', 'shield-check', 'Security', P::SECURITY_VIEW, 'lg', 120, 120, false, true, 'Firewall, fail2ban, SSH and panel sign-in failures, malware scanner, ModSecurity and the security audit.', 'security', 'Security'),
            new Widget('ssl_overview', 'SSL certificates', 'lock', 'Security', P::SSL_VIEW, 'md', 300, 300, false, true, 'Certificates close to expiry or expired, automatic renewal, and websites served without a matching certificate.', 'ssl.hosts', 'SSL'),
            new Widget('top_processes', 'Top processes', 'cpu', 'Performance', P::MONITORING_VIEW, 'lg', 15, 10, false, true, 'The five busiest processes by CPU and by memory.', 'monitoring.processes', 'Processes'),
            new Widget('services_overview', 'Service health', 'heart-pulse', 'System Health', P::SERVICES_VIEW, 'md', 30, 15, false, true, 'How many enabled services run, are stopped or have failed, and how many ran over time.', 'services', 'Services'),
            new Widget('database_overview', 'Databases', 'database', 'Databases', P::DATABASES_VIEW, 'lg', 60, 30, false, true, 'MariaDB/MySQL and PostgreSQL: connections against the limit, slow queries, cache hit ratio and lock waits.', 'databases.server', 'Database server'),
            new Widget('cache_overview', 'Redis & Memcached', 'lightning-charge', 'Performance', P::REDIS_MANAGE, 'md', 60, 30, false, true, 'Memory use, hit ratio, clients and evictions of the caches.', 'redis', 'Redis'),
            new Widget('capacity_forecast', 'Capacity forecast', 'graph-up-arrow', 'Performance', P::MONITORING_VIEW, 'lg', 600, 300, false, true, 'When the disk or memory is likely to fill at the current pace, and CPU and load against the week before. Needs about a week of measurements.', 'monitoring', 'Monitoring'),
            new Widget('web_stack', 'Nginx & PHP-FPM', 'server', 'System Health', P::SERVER_VIEW, 'md', 120, 60, false, true, 'Nginx state and, per PHP version in use, how often a pool ran out of workers in the last 24 hours.', 'nginx', 'Nginx'),
            new Widget('traffic_overview', 'Traffic', 'bar-chart-line', 'Performance', P::MONITORING_VIEW, 'lg', 300, 120, false, true, 'Network use over the last 24 hours and the websites that sent the most this month.', 'monitoring', 'Monitoring'),
            new Widget('dns_mail', 'DNS & mail', 'envelope-at', 'DNS & Email', P::SERVER_VIEW, 'md', 120, 60, false, true, 'The nameserver, the mail services and the mail queue.', 'dns', 'DNS'),
            new Widget('backup_health', 'Backup health', 'shield-check', 'Backups', P::BACKUPS_VIEW, 'lg', 120, 60, false, true, 'Websites without a recent backup, the latest failure, schedules, off-server copies and encryption.', 'backups', 'Backups'),
            new Widget('backups', 'Backups', 'cloud-arrow-up', 'Backups', P::BACKUPS_VIEW, 'md', 0, 60, true, true, 'Completed, failed and running backups.', 'backups', 'Backups'),
            new Widget('system_updates', 'System updates', 'arrow-up-circle', 'System Health', P::PACKAGES_MANAGE, 'md', 300, 300, false, true, 'Package updates waiting (security ones separately) and whether a restart is needed.', 'packages', 'Packages'),
            new Widget('deployments', 'Deployments', 'rocket-takeoff', 'DevOps', P::DEPLOY_MANAGE, 'md', 120, 60, false, true, 'Deployments in the last seven days, any running now, and the latest failure.', 'websites', 'Websites'),
            new Widget('request_trends', 'Request trends', 'graph-up', 'Websites', P::MONITORING_VIEW, 'lg', 300, 300, false, true, 'Requests to all websites per hour over the last 24 hours against the day before, the share of errors and the busiest websites, counted from their access logs.', 'websites', 'Websites'),
            new Widget('wordpress', 'WordPress', 'wordpress', 'Websites', P::WEBSITES_VIEW, 'md', 300, 120, false, true, 'WordPress installations: out-of-date core, plugin and theme updates, open issues.', 'wordpress', 'WordPress'),
        ];
        $by = [];
        foreach ($list as $w) {
            $by[$w->id] = $w;
        }
        return $by;
    }
}
