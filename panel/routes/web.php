<?php

declare(strict_types=1);

use WhaCP\Http\Controllers\AuditLogController;
use WhaCP\Http\Controllers\AuthController;
use WhaCP\Http\Controllers\DashboardController;
use WhaCP\Http\Controllers\DatabasesController;
use WhaCP\Http\Controllers\DiscoveryController;
use WhaCP\Http\Controllers\HealthController;
use WhaCP\Http\Controllers\DomainsController;
use WhaCP\Http\Controllers\ProfileController;
use WhaCP\Http\Controllers\RolesController;
use WhaCP\Http\Controllers\ServerController;
use WhaCP\Http\Controllers\SettingsController;
use WhaCP\Http\Controllers\SitesController;
use WhaCP\Http\Controllers\SslController;
use WhaCP\Http\Controllers\JobsController;
use WhaCP\Http\Controllers\PhpController;
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
    $r->get('/ssl', [DiscoveryController::class, 'ssl'], ['name' => 'ssl', 'permission' => P::SSL_VIEW]);
    $r->get('/cron', [DiscoveryController::class, 'cron'], ['name' => 'cron', 'permission' => P::CRON_VIEW]);
    $r->get('/discovery', [DiscoveryController::class, 'index'], ['name' => 'discovery', 'permission' => P::WEBSITES_VIEW]);
    $r->post('/discovery/run', [DiscoveryController::class, 'run'], ['name' => 'discovery.run', 'permission' => P::DISCOVERY_RUN]);

    // Server
    $r->get('/server', [ServerController::class, 'index'], ['name' => 'server', 'permission' => P::SERVER_VIEW]);
    $r->get('/services', [ServerController::class, 'services'], ['name' => 'services', 'permission' => P::SERVICES_VIEW]);
    $r->post('/services/control', [ServicesController::class, 'control'], ['name' => 'services.control', 'permission' => P::SERVICES_MANAGE]);
    $r->get('/system-users', [ServerController::class, 'systemUsers'], ['name' => 'system_users', 'permission' => P::SYSTEM_USERS_VIEW]);

    // Administration
    $r->get('/users', [UsersController::class, 'index'], ['name' => 'users', 'permission' => P::USERS_VIEW]);
    $r->get('/users/new', [UsersController::class, 'create'], ['name' => 'users.create', 'permission' => P::USERS_MANAGE]);
    $r->post('/users', [UsersController::class, 'store'], ['name' => 'users.store', 'permission' => P::USERS_MANAGE]);
    $r->get('/users/{id}/edit', [UsersController::class, 'edit'], ['name' => 'users.edit', 'permission' => P::USERS_MANAGE]);
    $r->post('/users/{id}', [UsersController::class, 'update'], ['name' => 'users.update', 'permission' => P::USERS_MANAGE]);
    $r->post('/users/{id}/delete', [UsersController::class, 'destroy'], ['name' => 'users.delete', 'permission' => P::USERS_MANAGE]);
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
