<?php

declare(strict_types=1);

use WhaCP\Http\Controllers\AuditLogController;
use WhaCP\Http\Controllers\AuthController;
use WhaCP\Http\Controllers\DashboardController;
use WhaCP\Http\Controllers\DiscoveryController;
use WhaCP\Http\Controllers\HealthController;
use WhaCP\Http\Controllers\DomainsController;
use WhaCP\Http\Controllers\ProfileController;
use WhaCP\Http\Controllers\RolesController;
use WhaCP\Http\Controllers\ServerController;
use WhaCP\Http\Controllers\SettingsController;
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
    $r->get('/websites/{id}', [WebsitesController::class, 'show'], ['name' => 'websites.show', 'permission' => P::WEBSITES_VIEW]);
    $r->get('/domains', [DomainsController::class, 'index'], ['name' => 'domains', 'permission' => P::WEBSITES_VIEW]);
    $r->get('/php', [DiscoveryController::class, 'php'], ['name' => 'php', 'permission' => P::PHP_VIEW]);
    $r->get('/databases', [DiscoveryController::class, 'databases'], ['name' => 'databases', 'permission' => P::DATABASES_VIEW]);
    $r->get('/ssl', [DiscoveryController::class, 'ssl'], ['name' => 'ssl', 'permission' => P::SSL_VIEW]);
    $r->get('/cron', [DiscoveryController::class, 'cron'], ['name' => 'cron', 'permission' => P::CRON_VIEW]);
    $r->get('/discovery', [DiscoveryController::class, 'index'], ['name' => 'discovery', 'permission' => P::WEBSITES_VIEW]);
    $r->post('/discovery/run', [DiscoveryController::class, 'run'], ['name' => 'discovery.run', 'permission' => P::DISCOVERY_RUN]);

    // Server
    $r->get('/server', [ServerController::class, 'index'], ['name' => 'server', 'permission' => P::SERVER_VIEW]);
    $r->get('/services', [ServerController::class, 'services'], ['name' => 'services', 'permission' => P::SERVICES_VIEW]);
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
