<?php

declare(strict_types=1);

namespace WhaCP\Security;

/**
 * Canonical permission catalogue and default role grants.
 *
 * Permissions are code-defined; the database mirrors them so roles can be
 * assigned in the UI. Add new permissions here and run `bin/whacp migrate`.
 */
final class Permissions
{
    public const DASHBOARD_VIEW = 'dashboard.view';
    public const SERVER_VIEW = 'server.view';
    public const SERVICES_VIEW = 'services.view';
    public const WEBSITES_VIEW = 'websites.view';
    public const PHP_VIEW = 'php.view';
    public const DATABASES_VIEW = 'databases.view';
    public const SSL_VIEW = 'ssl.view';
    public const CRON_VIEW = 'cron.view';
    public const SYSTEM_USERS_VIEW = 'system_users.view';
    public const DISCOVERY_RUN = 'discovery.run';
    public const USERS_VIEW = 'users.view';
    public const USERS_MANAGE = 'users.manage';
    public const ROLES_MANAGE = 'roles.manage';
    public const SETTINGS_VIEW = 'settings.view';
    public const SETTINGS_MANAGE = 'settings.manage';
    public const AUDIT_VIEW = 'audit.view';

    /** @return array<string, array{label: string, group: string}> */
    public static function all(): array
    {
        return [
            self::DASHBOARD_VIEW => ['label' => 'View dashboard', 'group' => 'Overview'],
            self::SERVER_VIEW => ['label' => 'View server information', 'group' => 'Server'],
            self::SERVICES_VIEW => ['label' => 'View services', 'group' => 'Server'],
            self::SYSTEM_USERS_VIEW => ['label' => 'View system accounts', 'group' => 'Server'],
            self::WEBSITES_VIEW => ['label' => 'View websites', 'group' => 'Hosting'],
            self::PHP_VIEW => ['label' => 'View PHP versions and pools', 'group' => 'Hosting'],
            self::DATABASES_VIEW => ['label' => 'View databases', 'group' => 'Hosting'],
            self::SSL_VIEW => ['label' => 'View certificates', 'group' => 'Hosting'],
            self::CRON_VIEW => ['label' => 'View cron jobs', 'group' => 'Hosting'],
            self::DISCOVERY_RUN => ['label' => 'Run server discovery', 'group' => 'Hosting'],
            self::USERS_VIEW => ['label' => 'View panel users', 'group' => 'Administration'],
            self::USERS_MANAGE => ['label' => 'Create, edit and delete panel users', 'group' => 'Administration'],
            self::ROLES_MANAGE => ['label' => 'Create and edit custom roles', 'group' => 'Administration'],
            self::SETTINGS_VIEW => ['label' => 'View settings', 'group' => 'Administration'],
            self::SETTINGS_MANAGE => ['label' => 'Change settings', 'group' => 'Administration'],
            self::AUDIT_VIEW => ['label' => 'View audit log', 'group' => 'Administration'],
        ];
    }

    /**
     * Default system roles. "*" grants every permission.
     *
     * @return array<string, array{label: string, description: string, permissions: list<string>}>
     */
    public static function defaultRoles(): array
    {
        return [
            'admin' => [
                'label' => 'Administrator',
                'description' => 'Full access to every feature.',
                'permissions' => ['*'],
            ],
            'operator' => [
                'label' => 'Operator',
                'description' => 'Operates the server: can view everything and run discovery, but cannot manage users or settings.',
                'permissions' => [
                    self::DASHBOARD_VIEW, self::SERVER_VIEW, self::SERVICES_VIEW, self::SYSTEM_USERS_VIEW,
                    self::WEBSITES_VIEW, self::PHP_VIEW, self::DATABASES_VIEW, self::SSL_VIEW, self::CRON_VIEW,
                    self::DISCOVERY_RUN, self::AUDIT_VIEW,
                ],
            ],
            'viewer' => [
                'label' => 'Viewer',
                'description' => 'Read-only access to the dashboard and hosting inventory.',
                'permissions' => [
                    self::DASHBOARD_VIEW, self::SERVER_VIEW, self::SERVICES_VIEW,
                    self::WEBSITES_VIEW, self::PHP_VIEW, self::DATABASES_VIEW, self::SSL_VIEW,
                ],
            ],
        ];
    }
}
