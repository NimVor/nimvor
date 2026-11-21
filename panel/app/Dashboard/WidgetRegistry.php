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
        $list = [
            new Widget('attention', 'Needs attention', 'heart-pulse', 'Overview', P::WEBSITES_VIEW, 'md', 0, 30, true, true, 'Websites with something to fix: mail records, certificates, backups.', 'sites.healthcheck', 'Site health'),
            new Widget('disk', 'Disk', 'hdd', 'System Health', P::SERVER_VIEW, 'md', 0, 60, true, true, 'Space on each filesystem and where it goes.', 'server.disk', 'Disk usage'),
            new Widget('backups', 'Backups', 'cloud-arrow-up', 'Backups', P::BACKUPS_VIEW, 'md', 0, 60, true, true, 'Completed, failed and running backups.', 'backups', 'Backups'),
            new Widget('top_processes', 'Top processes', 'cpu', 'Performance', P::MONITORING_VIEW, 'lg', 15, 10, false, true, 'The five busiest processes by CPU and by memory.', 'monitoring.processes', 'Processes'),
            new Widget('website_health', 'Website health', 'activity', 'Websites', P::WEBSITES_VIEW, 'lg', 60, 30, false, true, 'Real HTTP checks of every website: up, failing, slow, uptime over 24 hours.', 'websites', 'Websites'),
            new Widget('security_overview', 'Security overview', 'shield-check', 'Security', P::SECURITY_VIEW, 'lg', 120, 120, false, true, 'Firewall, fail2ban, SSH and panel sign-in failures, malware scanner, ModSecurity and the security audit.', 'security', 'Security'),
            new Widget('ssl_overview', 'SSL certificates', 'lock', 'Security', P::SSL_VIEW, 'md', 300, 300, false, true, 'Certificates close to expiry or expired, automatic renewal, and websites served without a matching certificate.', 'ssl.hosts', 'SSL'),
        ];
        $by = [];
        foreach ($list as $w) {
            $by[$w->id] = $w;
        }
        return $by;
    }
}
