<?php

declare(strict_types=1);

namespace WhaCP\Tests\Integration;

use WhaCP\Repositories\AuditLogRepository;
use WhaCP\Repositories\SettingRepository;
use WhaCP\Security\Authorizer;
use WhaCP\Security\Permissions;
use WhaCP\Services\SettingsService;
use WhaCP\Support\ValidationException;
use WhaCP\Tests\Support\DatabaseTestCase;

final class RbacAndSettingsTest extends DatabaseTestCase
{
    public function testDefaultRoleGrants(): void
    {
        $authz = new Authorizer($this->roles);
        $admin = $this->users->find($this->createUser('adm', 'admin'));
        $viewer = $this->users->find($this->createUser('view', 'viewer'));
        $operator = $this->users->find($this->createUser('op', 'operator'));

        foreach (array_keys(Permissions::all()) as $perm) {
            self::assertTrue($authz->can($admin, $perm), $perm);
        }
        self::assertTrue($authz->can($viewer, Permissions::WEBSITES_VIEW));
        self::assertFalse($authz->can($viewer, Permissions::USERS_MANAGE));
        self::assertFalse($authz->can($viewer, Permissions::DISCOVERY_RUN));
        self::assertTrue($authz->can($operator, Permissions::DISCOVERY_RUN));
        self::assertFalse($authz->can($operator, Permissions::SETTINGS_MANAGE));
        self::assertFalse($authz->can(null, Permissions::DASHBOARD_VIEW));

        $viewer['is_active'] = 0;
        self::assertFalse($authz->can($viewer, Permissions::DASHBOARD_VIEW));
    }

    public function testSyncDefaultsIsIdempotent(): void
    {
        $before = $this->db->fetchAll('SELECT * FROM role_permissions ORDER BY role_id, permission_id');
        $this->roles->syncDefaults();
        $this->roles->syncDefaults();
        self::assertSame($before, $this->db->fetchAll('SELECT * FROM role_permissions ORDER BY role_id, permission_id'));
    }

    public function testSettingsValidationAndAudit(): void
    {
        $settings = new SettingsService(new SettingRepository($this->db), $this->audit);
        self::assertSame(30, $settings->int('session_idle_minutes'));

        $settings->update(['session_idle_minutes' => '45', 'panel_name' => 'My Panel', 'unknown' => 'ignored'], null);
        self::assertSame(45, $settings->int('session_idle_minutes'));
        self::assertSame('My Panel', $settings->string('panel_name'));
        self::assertSame('settings.update', $this->auditRows()[0]['action']);

        try {
            $settings->update(['session_idle_minutes' => '1', 'panel_name' => ''], null);
            self::fail('expected validation error');
        } catch (ValidationException $e) {
            self::assertEqualsCanonicalizing(['session_idle_minutes', 'panel_name'], array_keys($e->errors()));
        }
        self::assertSame(45, $settings->int('session_idle_minutes'), 'invalid input must not be stored');
    }

    public function testAuditSearchFiltersAndEscapesWildcards(): void
    {
        $this->audit->record('user.create', 'user', '1', 'success', ['username' => 'x_y']);
        $this->audit->record('user.delete', 'user', '2', 'success');
        $this->audit->record('auth.login', 'user', '3', 'failure');
        $repo = new AuditLogRepository($this->db);

        self::assertSame(2, $repo->search(['action' => 'user.'], 1, 50)['total']);
        self::assertSame(1, $repo->search(['result' => 'failure'], 1, 50)['total']);
        self::assertSame(0, $repo->search(['action' => '%'], 1, 50)['total'], '% must be literal');
        self::assertSame(1, $repo->search(['q' => 'x_y'], 1, 50)['total']);
        $page = $repo->search([], 2, 2);
        self::assertSame(3, $page['total']);
        self::assertCount(1, $page['rows']);
    }
}
