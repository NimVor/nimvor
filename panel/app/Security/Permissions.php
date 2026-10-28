<?php

declare(strict_types=1);

namespace NimVor\Security;

/**
 * Canonical permission catalogue and default role grants.
 *
 * Permissions are code-defined; the database mirrors them so roles can be
 * assigned in the UI. Add new permissions here and run `bin/nimvor migrate`.
 *
 * Roles have a scope: "all" roles see every object on a server, "own" roles
 * (resellers and hosting customers) only see objects they own or, for
 * resellers, objects owned by their customers.
 */
final class Permissions
{
    public const DASHBOARD_VIEW = 'dashboard.view';
    public const SERVER_VIEW = 'server.view';
    public const SERVER_MANAGE = 'server.manage';
    public const SERVICES_VIEW = 'services.view';
    public const SERVICES_MANAGE = 'services.manage';
    public const SYSTEM_USERS_VIEW = 'system_users.view';
    public const MONITORING_VIEW = 'monitoring.view';
    public const NOTIFICATIONS_VIEW = 'notifications.view';
    public const PACKAGES_MANAGE = 'packages.manage';
    public const SERVERS_MANAGE = 'servers.manage';
    public const MONGO_VIEW = 'mongo.view';
    public const MONGO_MANAGE = 'mongo.manage';

    public const WEBSITES_VIEW = 'websites.view';
    public const WEBSITES_MANAGE = 'websites.manage';
    public const PHP_VIEW = 'php.view';
    public const PHP_MANAGE = 'php.manage';
    public const DATABASES_VIEW = 'databases.view';
    public const DATABASES_MANAGE = 'databases.manage';
    public const SSL_VIEW = 'ssl.view';
    public const SSL_MANAGE = 'ssl.manage';
    public const FILES_MANAGE = 'files.manage';
    public const LOGS_VIEW = 'logs.view';
    public const CRON_VIEW = 'cron.view';
    public const CRON_MANAGE = 'cron.manage';
    public const WORKERS_MANAGE = 'workers.manage';
    public const DEPLOY_MANAGE = 'deploy.manage';
    public const REDIS_MANAGE = 'redis.manage';
    public const FTP_MANAGE = 'ftp.manage';
    public const DNS_MANAGE = 'dns.manage';
    public const MAIL_MANAGE = 'mail.manage';
    public const BACKUPS_VIEW = 'backups.view';
    public const BACKUPS_MANAGE = 'backups.manage';
    public const APPS_INSTALL = 'apps.install';
    public const DISCOVERY_RUN = 'discovery.run';
    public const IMPORT_RUN = 'import.run';

    public const SECURITY_VIEW = 'security.view';
    public const SECURITY_MANAGE = 'security.manage';

    public const USERS_VIEW = 'users.view';
    public const USERS_MANAGE = 'users.manage';
    public const ROLES_MANAGE = 'roles.manage';
    public const PLANS_MANAGE = 'plans.manage';
    public const SETTINGS_VIEW = 'settings.view';
    public const SETTINGS_MANAGE = 'settings.manage';
    public const AUDIT_VIEW = 'audit.view';
    public const API_TOKENS = 'api.tokens';
    public const PLUGINS_MANAGE = 'plugins.manage';

    /** @return array<string, array{label: string, group: string}> */
    public static function all(): array
    {
        return [
            self::DASHBOARD_VIEW => ['label' => 'View dashboard', 'group' => 'Overview'],
            self::MONITORING_VIEW => ['label' => 'View monitoring, live load and running processes', 'group' => 'Overview'],
            self::NOTIFICATIONS_VIEW => ['label' => 'See notifications (job results and alerts, within the role\'s scope)', 'group' => 'Overview'],

            self::WEBSITES_VIEW => ['label' => 'View websites', 'group' => 'Hosting'],
            self::WEBSITES_MANAGE => ['label' => 'Create, change and delete websites', 'group' => 'Hosting'],
            self::PHP_VIEW => ['label' => 'View PHP versions and pools', 'group' => 'Hosting'],
            self::PHP_MANAGE => ['label' => 'Install and control PHP versions', 'group' => 'Hosting'],
            self::DATABASES_VIEW => ['label' => 'View databases', 'group' => 'Hosting'],
            self::DATABASES_MANAGE => ['label' => 'Manage databases and database users', 'group' => 'Hosting'],
            self::SSL_VIEW => ['label' => 'View certificates', 'group' => 'Hosting'],
            self::SSL_MANAGE => ['label' => 'Issue, renew and remove certificates', 'group' => 'Hosting'],
            self::FILES_MANAGE => ['label' => 'Use the file manager and a website\'s terminal', 'group' => 'Hosting'],
            self::LOGS_VIEW => ['label' => 'View logs (web, PHP, mail, database, security, system)', 'group' => 'Hosting'],
            self::CRON_VIEW => ['label' => 'View cron jobs', 'group' => 'Hosting'],
            self::CRON_MANAGE => ['label' => 'Manage cron jobs', 'group' => 'Hosting'],
            self::WORKERS_MANAGE => ['label' => 'Manage background workers', 'group' => 'Hosting'],
            self::DEPLOY_MANAGE => ['label' => 'Manage Git deployments', 'group' => 'Hosting'],
            self::REDIS_MANAGE => ['label' => 'Manage Redis and Memcached', 'group' => 'Hosting'],
            self::FTP_MANAGE => ['label' => 'Manage FTP accounts and SFTP access of websites', 'group' => 'Hosting'],
            self::DNS_MANAGE => ['label' => 'Manage DNS zones and records', 'group' => 'Hosting'],
            self::MAIL_MANAGE => ['label' => 'Manage email domains, mailboxes and aliases', 'group' => 'Hosting'],
            self::BACKUPS_VIEW => ['label' => 'View backups', 'group' => 'Hosting'],
            self::BACKUPS_MANAGE => ['label' => 'Create, restore and delete backups', 'group' => 'Hosting'],
            self::APPS_INSTALL => ['label' => 'Install applications', 'group' => 'Hosting'],
            self::DISCOVERY_RUN => ['label' => 'Run server discovery', 'group' => 'Hosting'],
            self::IMPORT_RUN => ['label' => 'Import existing websites', 'group' => 'Hosting'],

            self::SERVER_VIEW => ['label' => 'View server information, IP addresses, mail and database server status', 'group' => 'Server'],
            self::SERVER_MANAGE => ['label' => 'Configure the server: reboot, update, host name, IP addresses, mail and database servers, resource limits, processes, server backups', 'group' => 'Server'],
            self::SERVICES_VIEW => ['label' => 'View services', 'group' => 'Server'],
            self::SERVICES_MANAGE => ['label' => 'Start, stop and restart services', 'group' => 'Server'],
            self::SYSTEM_USERS_VIEW => ['label' => 'View system accounts', 'group' => 'Server'],
            self::PACKAGES_MANAGE => ['label' => 'Install and update packages and server components', 'group' => 'Server'],
            self::SERVERS_MANAGE => ['label' => 'Add and remove servers', 'group' => 'Server'],
            self::MONGO_VIEW => ['label' => 'View the MongoDB server, its databases, users and backups, and browse data', 'group' => 'Server'],
            self::MONGO_MANAGE => ['label' => 'Manage MongoDB: databases, users, data, configuration, and backups/restores', 'group' => 'Server'],
            self::SECURITY_VIEW => ['label' => 'View security status, firewall events and scans', 'group' => 'Security'],
            self::SECURITY_MANAGE => ['label' => 'Change security: firewall, SSH, ModSecurity, brute-force protection, malware scans, policies, the root terminal', 'group' => 'Security'],

            self::USERS_VIEW => ['label' => 'View panel users', 'group' => 'Administration'],
            self::USERS_MANAGE => ['label' => 'Create, edit, suspend and terminate users and hosting accounts; bandwidth limits', 'group' => 'Administration'],
            self::ROLES_MANAGE => ['label' => 'Create and edit roles (never beyond one\'s own permissions)', 'group' => 'Administration'],
            self::PLANS_MANAGE => ['label' => 'Manage hosting plans and feature lists', 'group' => 'Administration'],
            self::SETTINGS_VIEW => ['label' => 'View settings', 'group' => 'Administration'],
            self::SETTINGS_MANAGE => ['label' => 'Change settings', 'group' => 'Administration'],
            self::AUDIT_VIEW => ['label' => 'View audit log', 'group' => 'Administration'],
            self::API_TOKENS => ['label' => 'Create personal API tokens', 'group' => 'Administration'],
            self::PLUGINS_MANAGE => ['label' => 'Install and enable plugins', 'group' => 'Administration'],
        ];
    }

    /** Permissions a hosting customer may hold for their own websites. */
    private const HOSTING_SELF = [
        self::DASHBOARD_VIEW, self::NOTIFICATIONS_VIEW, self::WEBSITES_VIEW, self::WEBSITES_MANAGE, self::PHP_VIEW,
        self::DATABASES_VIEW, self::DATABASES_MANAGE, self::SSL_VIEW, self::SSL_MANAGE,
        self::FILES_MANAGE, self::LOGS_VIEW, self::CRON_VIEW, self::CRON_MANAGE, self::WORKERS_MANAGE,
        self::DEPLOY_MANAGE, self::BACKUPS_VIEW, self::BACKUPS_MANAGE, self::APPS_INSTALL, self::API_TOKENS, self::FTP_MANAGE, self::DNS_MANAGE, self::MAIL_MANAGE,
    ];

    /**
     * What an own-scope role (reseller, hosting customer) may be given:
     * their hosting permissions and, for resellers, their customers and
     * plans. Server-wide permissions would reach past their own objects.
     *
     * @return list<string>
     */
    public static function ownScopeAllowed(): array
    {
        return array_merge(self::HOSTING_SELF, [self::USERS_VIEW, self::USERS_MANAGE, self::PLANS_MANAGE]);
    }

    /**
     * Default system roles. "*" grants every permission.
     *
     * @return array<string, array{label: string, description: string, scope: string, permissions: list<string>}>
     */
    public static function defaultRoles(): array
    {
        return [
            'admin' => [
                'label' => 'Administrator',
                'description' => 'Full access to every feature.',
                'scope' => 'all',
                'permissions' => ['*'],
            ],
            'operator' => [
                'label' => 'Operator',
                'description' => 'Runs day-to-day hosting: websites, PHP, databases, certificates, files, jobs and services. Cannot manage users, settings, security or reboot the server.',
                'scope' => 'all',
                'permissions' => array_merge(self::HOSTING_SELF, [
                    self::MONITORING_VIEW, self::SERVER_VIEW, self::SERVICES_VIEW, self::SERVICES_MANAGE, self::SYSTEM_USERS_VIEW,
                    self::PHP_MANAGE, self::REDIS_MANAGE, self::DISCOVERY_RUN, self::SECURITY_VIEW, self::AUDIT_VIEW,
                    self::MONGO_VIEW, self::MONGO_MANAGE,
                ]),
            ],
            'viewer' => [
                'label' => 'Viewer',
                'description' => 'Read-only access to the dashboard and hosting inventory.',
                'scope' => 'all',
                'permissions' => [
                    self::DASHBOARD_VIEW, self::NOTIFICATIONS_VIEW, self::MONITORING_VIEW, self::SERVER_VIEW, self::SERVICES_VIEW,
                    self::WEBSITES_VIEW, self::PHP_VIEW, self::DATABASES_VIEW, self::SSL_VIEW, self::CRON_VIEW, self::BACKUPS_VIEW, self::MONGO_VIEW,
                ],
            ],
            'reseller' => [
                'label' => 'Reseller',
                'description' => 'Manages their own customers and those customers\' websites within plan limits.',
                'scope' => 'own',
                'permissions' => array_merge(self::HOSTING_SELF, [self::USERS_VIEW, self::USERS_MANAGE, self::PLANS_MANAGE]),
            ],
            'customer' => [
                'label' => 'Hosting customer',
                'description' => 'Manages only their own websites, databases, files and jobs, within plan limits.',
                'scope' => 'own',
                'permissions' => self::HOSTING_SELF,
            ],
        ];
    }
}
