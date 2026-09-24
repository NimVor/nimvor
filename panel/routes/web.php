<?php

declare(strict_types=1);

use WhaCP\Http\Controllers\AuditLogController;
use WhaCP\Http\Controllers\AuthController;
use WhaCP\Http\Controllers\BackupsController;
use WhaCP\Http\Controllers\CronController;
use WhaCP\Http\Controllers\DeployController;
use WhaCP\Http\Controllers\WorkersController;
use WhaCP\Http\Controllers\DashboardController;
use WhaCP\Http\Controllers\DatabasesController;
use WhaCP\Http\Controllers\DiscoveryController;
use WhaCP\Http\Controllers\HealthController;
use WhaCP\Http\Controllers\DomainsController;
use WhaCP\Http\Controllers\FilesController;
use WhaCP\Http\Controllers\ProfileController;
use WhaCP\Http\Controllers\RolesController;
use WhaCP\Http\Controllers\ServerController;
use WhaCP\Http\Controllers\SettingsController;
use WhaCP\Http\Controllers\SitesController;
use WhaCP\Http\Controllers\SslController;
use WhaCP\Http\Controllers\JobsController;
use WhaCP\Http\Controllers\LogsController;
use WhaCP\Http\Controllers\MonitoringController;
use WhaCP\Http\Controllers\PhpController;
use WhaCP\Http\Controllers\PlansController;
use WhaCP\Http\Controllers\RedisController;
use WhaCP\Http\Controllers\SecurityController;
use WhaCP\Http\Controllers\ServersController;
use WhaCP\Http\Controllers\SystemController;
use WhaCP\Http\Controllers\ServicesController;
use WhaCP\Http\Controllers\UsersController;
use WhaCP\Http\Controllers\WebsitesController;
use WhaCP\Http\Router;
use WhaCP\Security\Permissions as P;

return static function (Router $r): void {
    // Public
    $r->get('/login', [AuthController::class, 'show'], ['name' => 'login', 'auth' => false]);
    $r->post('/login', [AuthController::class, 'login'], ['name' => 'login.submit', 'auth' => false]);
    $r->get('/healthz', [HealthController::class, 'show'], ['name' => 'health', 'auth' => false]);
    // Signed Git push webhooks (authenticated by HMAC/token, not a session).
    $r->post('/hooks/deploy/{token}', [DeployController::class, 'webhook'], ['name' => 'hooks.deploy', 'auth' => false, 'csrf' => false]);
    $r->post('/logout', [AuthController::class, 'logout'], ['name' => 'logout']);

    // Overview
    $r->get('/', [DashboardController::class, 'index'], ['name' => 'dashboard', 'permission' => P::DASHBOARD_VIEW]);
    $r->get('/dashboard/live', [DashboardController::class, 'live'], ['name' => 'dashboard.live', 'permission' => P::DASHBOARD_VIEW]);

    // Hosting inventory (read-only discovery)
    $r->get('/websites', [WebsitesController::class, 'index'], ['name' => 'websites', 'permission' => P::WEBSITES_VIEW]);
    $r->get('/websites/new', [SitesController::class, 'create'], ['name' => 'sites.create', 'permission' => P::WEBSITES_MANAGE]);
    $r->post('/websites/new', [SitesController::class, 'store'], ['name' => 'sites.store', 'permission' => P::WEBSITES_MANAGE]);
    $r->get('/sites/{id}', [SitesController::class, 'show'], ['name' => 'sites.show', 'permission' => P::WEBSITES_VIEW]);
    $r->get('/sites/{id}/settings', [SitesController::class, 'edit'], ['name' => 'sites.edit', 'permission' => P::WEBSITES_MANAGE]);
    $r->post('/sites/{id}/settings', [SitesController::class, 'update'], ['name' => 'sites.update', 'permission' => P::WEBSITES_MANAGE]);
    $r->post('/sites/{id}/toggle', [SitesController::class, 'toggle'], ['name' => 'sites.toggle', 'permission' => P::WEBSITES_MANAGE]);
    $r->post('/sites/{id}/restart-php', [SitesController::class, 'restartPhp'], ['name' => 'sites.restart_php', 'permission' => P::WEBSITES_MANAGE]);
    $r->post('/sites/{id}/reapply', [SitesController::class, 'reapply'], ['name' => 'sites.reapply', 'permission' => P::WEBSITES_MANAGE]);
    $r->post('/sites/{id}/delete', [SitesController::class, 'destroy'], ['name' => 'sites.delete', 'permission' => P::WEBSITES_MANAGE]);
    $r->get('/sites/{id}/ssl', [SslController::class, 'show'], ['name' => 'sites.ssl', 'permission' => P::SSL_VIEW]);
    $r->post('/sites/{id}/ssl/issue', [SslController::class, 'issue'], ['name' => 'sites.ssl.issue', 'permission' => P::SSL_MANAGE]);
    $r->post('/sites/{id}/ssl/custom', [SslController::class, 'custom'], ['name' => 'sites.ssl.custom', 'permission' => P::SSL_MANAGE]);
    $r->post('/sites/{id}/ssl/renew', [SslController::class, 'renew'], ['name' => 'sites.ssl.renew', 'permission' => P::SSL_MANAGE]);
    $r->post('/sites/{id}/ssl/disable', [SslController::class, 'disable'], ['name' => 'sites.ssl.disable', 'permission' => P::SSL_MANAGE]);
    $r->post('/sites/{id}/ssl/options', [SslController::class, 'options'], ['name' => 'sites.ssl.options', 'permission' => P::SSL_MANAGE]);
    $r->get('/sites/{id}/files', [FilesController::class, 'show'], ['name' => 'sites.files', 'permission' => P::FILES_MANAGE]);
    $r->post('/sites/{id}/files/api', [FilesController::class, 'api'], ['name' => 'sites.files.api', 'permission' => P::FILES_MANAGE]);
    $r->post('/sites/{id}/files/upload', [FilesController::class, 'upload'], ['name' => 'sites.files.upload', 'permission' => P::FILES_MANAGE]);
    $r->get('/sites/{id}/files/download', [FilesController::class, 'download'], ['name' => 'sites.files.download', 'permission' => P::FILES_MANAGE]);
    $r->get('/sites/{id}/logs', [LogsController::class, 'site'], ['name' => 'sites.logs', 'permission' => P::LOGS_VIEW]);
    $r->get('/sites/{id}/cron', [CronController::class, 'show'], ['name' => 'sites.cron', 'permission' => P::CRON_VIEW]);
    $r->post('/sites/{id}/cron', [CronController::class, 'store'], ['name' => 'sites.cron.store', 'permission' => P::CRON_MANAGE]);
    $r->post('/sites/{id}/cron/{job}/toggle', [CronController::class, 'toggle'], ['name' => 'sites.cron.toggle', 'permission' => P::CRON_MANAGE]);
    $r->post('/sites/{id}/cron/{job}/run', [CronController::class, 'run'], ['name' => 'sites.cron.run', 'permission' => P::CRON_MANAGE]);
    $r->post('/sites/{id}/cron/{job}/delete', [CronController::class, 'delete'], ['name' => 'sites.cron.delete', 'permission' => P::CRON_MANAGE]);
    $r->get('/sites/{id}/workers', [WorkersController::class, 'show'], ['name' => 'sites.workers', 'permission' => P::WORKERS_MANAGE]);
    $r->post('/sites/{id}/workers', [WorkersController::class, 'store'], ['name' => 'sites.workers.store', 'permission' => P::WORKERS_MANAGE]);
    $r->post('/sites/{id}/workers/{worker}/toggle', [WorkersController::class, 'toggle'], ['name' => 'sites.workers.toggle', 'permission' => P::WORKERS_MANAGE]);
    $r->post('/sites/{id}/workers/{worker}/restart', [WorkersController::class, 'restart'], ['name' => 'sites.workers.restart', 'permission' => P::WORKERS_MANAGE]);
    $r->post('/sites/{id}/workers/{worker}/delete', [WorkersController::class, 'delete'], ['name' => 'sites.workers.delete', 'permission' => P::WORKERS_MANAGE]);
    $r->get('/sites/{id}/git', [DeployController::class, 'show'], ['name' => 'sites.git', 'permission' => P::DEPLOY_MANAGE]);
    $r->post('/sites/{id}/git', [DeployController::class, 'configure'], ['name' => 'sites.git.configure', 'permission' => P::DEPLOY_MANAGE]);
    $r->post('/sites/{id}/git/deploy', [DeployController::class, 'run'], ['name' => 'sites.git.deploy', 'permission' => P::DEPLOY_MANAGE]);
    $r->post('/sites/{id}/git/rotate', [DeployController::class, 'rotate'], ['name' => 'sites.git.rotate', 'permission' => P::DEPLOY_MANAGE]);
    $r->post('/sites/{id}/git/remove', [DeployController::class, 'remove'], ['name' => 'sites.git.remove', 'permission' => P::DEPLOY_MANAGE]);
    $r->get('/sites/{id}/backups', [BackupsController::class, 'show'], ['name' => 'sites.backups', 'permission' => P::BACKUPS_VIEW]);
    $r->post('/sites/{id}/backups', [BackupsController::class, 'create'], ['name' => 'sites.backups.create', 'permission' => P::BACKUPS_MANAGE]);
    $r->post('/sites/{id}/backups/{backup}/restore', [BackupsController::class, 'restore'], ['name' => 'sites.backups.restore', 'permission' => P::BACKUPS_MANAGE]);
    $r->post('/sites/{id}/backups/{backup}/delete', [BackupsController::class, 'delete'], ['name' => 'sites.backups.delete', 'permission' => P::BACKUPS_MANAGE]);
    $r->get('/sites/{id}/backups/{backup}/download', [BackupsController::class, 'download'], ['name' => 'sites.backups.download', 'permission' => P::BACKUPS_MANAGE]);
    $r->post('/sites/{id}/backup-schedules', [BackupsController::class, 'storeSchedule'], ['name' => 'sites.backups.schedule', 'permission' => P::BACKUPS_MANAGE]);
    $r->post('/sites/{id}/backup-schedules/{schedule}/delete', [BackupsController::class, 'deleteSchedule'], ['name' => 'sites.backups.schedule_delete', 'permission' => P::BACKUPS_MANAGE]);
    $r->get('/backups', [BackupsController::class, 'index'], ['name' => 'backups', 'permission' => P::BACKUPS_VIEW]);
    $r->post('/backups/server', [BackupsController::class, 'createServer'], ['name' => 'backups.server', 'permission' => P::BACKUPS_MANAGE]);
    $r->post('/backups/server/{backup}/delete', [BackupsController::class, 'deleteServer'], ['name' => 'backups.server.delete', 'permission' => P::BACKUPS_MANAGE]);
    $r->get('/backups/server/{backup}/download', [BackupsController::class, 'downloadServer'], ['name' => 'backups.server.download', 'permission' => P::BACKUPS_MANAGE]);
    $r->post('/backups/storages', [BackupsController::class, 'storeStorage'], ['name' => 'backups.storages.store', 'permission' => P::BACKUPS_MANAGE]);
    $r->post('/backups/storages/{storage}/delete', [BackupsController::class, 'deleteStorage'], ['name' => 'backups.storages.delete', 'permission' => P::BACKUPS_MANAGE]);
    $r->post('/backups/schedules', [BackupsController::class, 'storeServerSchedule'], ['name' => 'backups.schedules.store', 'permission' => P::BACKUPS_MANAGE]);
    $r->post('/backups/schedules/{schedule}/delete', [BackupsController::class, 'deleteServerSchedule'], ['name' => 'backups.schedules.delete', 'permission' => P::BACKUPS_MANAGE]);
    $r->get('/monitoring', [MonitoringController::class, 'index'], ['name' => 'monitoring', 'permission' => P::MONITORING_VIEW]);
    $r->get('/logs', [LogsController::class, 'index'], ['name' => 'logs', 'permission' => P::LOGS_VIEW]);
    $r->get('/logs/tail', [LogsController::class, 'tail'], ['name' => 'logs.tail', 'permission' => P::LOGS_VIEW]);
    $r->get('/logs/download', [LogsController::class, 'download'], ['name' => 'logs.download', 'permission' => P::LOGS_VIEW]);
    $r->get('/jobs/{id}', [JobsController::class, 'show'], ['name' => 'jobs.show']);
    $r->get('/websites/{id}', [WebsitesController::class, 'show'], ['name' => 'websites.show', 'permission' => P::WEBSITES_VIEW]);
    $r->get('/domains', [DomainsController::class, 'index'], ['name' => 'domains', 'permission' => P::WEBSITES_VIEW]);
    $r->get('/php', [DiscoveryController::class, 'php'], ['name' => 'php', 'permission' => P::PHP_VIEW]);
    $r->post('/php/install', [PhpController::class, 'install'], ['name' => 'php.install', 'permission' => P::PHP_MANAGE]);
    $r->post('/php/{version}/toggle', [PhpController::class, 'toggle'], ['name' => 'php.toggle', 'permission' => P::PHP_MANAGE]);
    $r->get('/php/{version}/modules', [PhpController::class, 'modules'], ['name' => 'php.modules', 'permission' => P::PHP_VIEW]);
    $r->get('/databases', [DatabasesController::class, 'index'], ['name' => 'databases', 'permission' => P::DATABASES_VIEW]);
    $r->post('/databases', [DatabasesController::class, 'store'], ['name' => 'databases.store', 'permission' => P::DATABASES_MANAGE]);
    $r->post('/databases/{id}/drop', [DatabasesController::class, 'drop'], ['name' => 'databases.drop', 'permission' => P::DATABASES_MANAGE]);
    $r->post('/databases/{id}/dump', [DatabasesController::class, 'dump'], ['name' => 'databases.dump', 'permission' => P::DATABASES_MANAGE]);
    $r->post('/databases/{id}/grant', [DatabasesController::class, 'grant'], ['name' => 'databases.grant', 'permission' => P::DATABASES_MANAGE]);
    $r->post('/database-users', [DatabasesController::class, 'createUser'], ['name' => 'dbusers.store', 'permission' => P::DATABASES_MANAGE]);
    $r->post('/database-users/{id}/password', [DatabasesController::class, 'password'], ['name' => 'dbusers.password', 'permission' => P::DATABASES_MANAGE]);
    $r->post('/database-users/{id}/reveal', [DatabasesController::class, 'reveal'], ['name' => 'dbusers.reveal', 'permission' => P::DATABASES_MANAGE]);
    $r->post('/database-users/{id}/delete', [DatabasesController::class, 'deleteUser'], ['name' => 'dbusers.delete', 'permission' => P::DATABASES_MANAGE]);
    $r->get('/sites/{id}/databases', [DatabasesController::class, 'site'], ['name' => 'sites.databases', 'permission' => P::DATABASES_VIEW]);
    $r->get('/redis', [RedisController::class, 'index'], ['name' => 'redis', 'permission' => P::REDIS_MANAGE]);
    $r->post('/redis/configure', [RedisController::class, 'configure'], ['name' => 'redis.configure', 'permission' => P::REDIS_MANAGE]);
    $r->post('/redis/password', [RedisController::class, 'password'], ['name' => 'redis.password', 'permission' => P::REDIS_MANAGE]);
    $r->post('/redis/flush', [RedisController::class, 'flush'], ['name' => 'redis.flush', 'permission' => P::REDIS_MANAGE]);
    $r->post('/redis/users', [RedisController::class, 'createUser'], ['name' => 'redis.users.store', 'permission' => P::REDIS_MANAGE]);
    $r->post('/redis/users/delete', [RedisController::class, 'deleteUser'], ['name' => 'redis.users.delete', 'permission' => P::REDIS_MANAGE]);
    $r->get('/workers', [WorkersController::class, 'index'], ['name' => 'workers', 'permission' => P::WORKERS_MANAGE]);
    $r->get('/ssl', [DiscoveryController::class, 'ssl'], ['name' => 'ssl', 'permission' => P::SSL_VIEW]);
    $r->get('/cron', [DiscoveryController::class, 'cron'], ['name' => 'cron', 'permission' => P::CRON_VIEW]);
    $r->get('/discovery', [DiscoveryController::class, 'index'], ['name' => 'discovery', 'permission' => P::WEBSITES_VIEW]);
    $r->post('/discovery/run', [DiscoveryController::class, 'run'], ['name' => 'discovery.run', 'permission' => P::DISCOVERY_RUN]);

    // Server
    $r->get('/server', [ServerController::class, 'index'], ['name' => 'server', 'permission' => P::SERVER_VIEW]);
    $r->get('/services', [ServerController::class, 'services'], ['name' => 'services', 'permission' => P::SERVICES_VIEW]);
    $r->post('/services/control', [ServicesController::class, 'control'], ['name' => 'services.control', 'permission' => P::SERVICES_MANAGE]);
    $r->get('/packages', [SystemController::class, 'packages'], ['name' => 'packages', 'permission' => P::PACKAGES_MANAGE]);
    $r->post('/packages/install', [SystemController::class, 'install'], ['name' => 'packages.install', 'permission' => P::PACKAGES_MANAGE]);
    $r->post('/packages/refresh', [SystemController::class, 'refresh'], ['name' => 'packages.refresh', 'permission' => P::PACKAGES_MANAGE]);
    $r->post('/packages/upgrade', [SystemController::class, 'upgrade'], ['name' => 'packages.upgrade', 'permission' => P::PACKAGES_MANAGE]);
    $r->post('/server/hostname', [SystemController::class, 'hostname'], ['name' => 'server.hostname', 'permission' => P::SERVER_MANAGE]);
    $r->post('/server/timezone', [SystemController::class, 'timezone'], ['name' => 'server.timezone', 'permission' => P::SERVER_MANAGE]);
    $r->post('/server/reboot', [SystemController::class, 'reboot'], ['name' => 'server.reboot', 'permission' => P::SERVER_MANAGE]);
    $r->post('/server/reboot/cancel', [SystemController::class, 'cancelReboot'], ['name' => 'server.reboot_cancel', 'permission' => P::SERVER_MANAGE]);

    // Security
    $r->get('/security', [SecurityController::class, 'index'], ['name' => 'security', 'permission' => P::SECURITY_VIEW]);
    $r->get('/security/firewall', [SecurityController::class, 'firewall'], ['name' => 'security.firewall', 'permission' => P::SECURITY_VIEW]);
    $r->post('/security/firewall/rules', [SecurityController::class, 'addRule'], ['name' => 'security.firewall.add', 'permission' => P::SECURITY_MANAGE]);
    $r->post('/security/firewall/rules/delete', [SecurityController::class, 'deleteRule'], ['name' => 'security.firewall.delete', 'permission' => P::SECURITY_MANAGE]);
    $r->post('/security/firewall/toggle', [SecurityController::class, 'toggleFirewall'], ['name' => 'security.firewall.toggle', 'permission' => P::SECURITY_MANAGE]);
    $r->get('/security/ssh', [SecurityController::class, 'ssh'], ['name' => 'security.ssh', 'permission' => P::SECURITY_VIEW]);
    $r->post('/security/ssh', [SecurityController::class, 'configureSsh'], ['name' => 'security.ssh.configure', 'permission' => P::SECURITY_MANAGE]);
    $r->get('/security/fail2ban', [SecurityController::class, 'fail2ban'], ['name' => 'security.fail2ban', 'permission' => P::SECURITY_VIEW]);
    $r->post('/security/fail2ban', [SecurityController::class, 'configureFail2ban'], ['name' => 'security.fail2ban.configure', 'permission' => P::SECURITY_MANAGE]);
    $r->post('/security/fail2ban/unban', [SecurityController::class, 'unban'], ['name' => 'security.fail2ban.unban', 'permission' => P::SECURITY_MANAGE]);
    $r->post('/security/confirm', [SecurityController::class, 'confirm'], ['name' => 'security.confirm', 'permission' => P::SECURITY_MANAGE]);
    $r->post('/security/undo', [SecurityController::class, 'undo'], ['name' => 'security.undo', 'permission' => P::SECURITY_MANAGE]);
    $r->get('/servers', [ServersController::class, 'index'], ['name' => 'servers', 'permission' => P::SERVERS_MANAGE]);
    $r->post('/servers', [ServersController::class, 'store'], ['name' => 'servers.store', 'permission' => P::SERVERS_MANAGE]);
    $r->post('/servers/{id}/check', [ServersController::class, 'check'], ['name' => 'servers.check', 'permission' => P::SERVERS_MANAGE]);
    $r->post('/servers/{id}/delete', [ServersController::class, 'destroy'], ['name' => 'servers.delete', 'permission' => P::SERVERS_MANAGE]);
    $r->post('/servers/switch', [ServersController::class, 'switch'], ['name' => 'servers.switch']);
    $r->get('/system-users', [ServerController::class, 'systemUsers'], ['name' => 'system_users', 'permission' => P::SYSTEM_USERS_VIEW]);

    // Administration
    $r->get('/users', [UsersController::class, 'index'], ['name' => 'users', 'permission' => P::USERS_VIEW]);
    $r->get('/users/new', [UsersController::class, 'create'], ['name' => 'users.create', 'permission' => P::USERS_MANAGE]);
    $r->post('/users', [UsersController::class, 'store'], ['name' => 'users.store', 'permission' => P::USERS_MANAGE]);
    $r->get('/users/{id}/edit', [UsersController::class, 'edit'], ['name' => 'users.edit', 'permission' => P::USERS_MANAGE]);
    $r->post('/users/{id}', [UsersController::class, 'update'], ['name' => 'users.update', 'permission' => P::USERS_MANAGE]);
    $r->post('/users/{id}/delete', [UsersController::class, 'destroy'], ['name' => 'users.delete', 'permission' => P::USERS_MANAGE]);
    $r->get('/plans', [PlansController::class, 'index'], ['name' => 'plans', 'permission' => P::PLANS_MANAGE]);
    $r->post('/plans', [PlansController::class, 'store'], ['name' => 'plans.store', 'permission' => P::PLANS_MANAGE]);
    $r->post('/plans/{id}/delete', [PlansController::class, 'destroy'], ['name' => 'plans.delete', 'permission' => P::PLANS_MANAGE]);
    $r->get('/roles/new', [RolesController::class, 'create'], ['name' => 'roles.create', 'permission' => P::ROLES_MANAGE]);
    $r->post('/roles', [RolesController::class, 'store'], ['name' => 'roles.store', 'permission' => P::ROLES_MANAGE]);
    $r->get('/roles/{id}/edit', [RolesController::class, 'edit'], ['name' => 'roles.edit', 'permission' => P::USERS_VIEW]);
    $r->post('/roles/{id}', [RolesController::class, 'update'], ['name' => 'roles.update', 'permission' => P::ROLES_MANAGE]);
    $r->post('/roles/{id}/delete', [RolesController::class, 'destroy'], ['name' => 'roles.delete', 'permission' => P::ROLES_MANAGE]);
    $r->get('/settings', [SettingsController::class, 'index'], ['name' => 'settings', 'permission' => P::SETTINGS_VIEW]);
    $r->post('/settings', [SettingsController::class, 'update'], ['name' => 'settings.update', 'permission' => P::SETTINGS_MANAGE]);
    $r->get('/audit', [AuditLogController::class, 'index'], ['name' => 'audit', 'permission' => P::AUDIT_VIEW]);

    // Own account (any signed-in user)
    $r->get('/profile', [ProfileController::class, 'show'], ['name' => 'profile']);
    $r->post('/profile/password', [ProfileController::class, 'password'], ['name' => 'profile.password']);
};
