<?php

declare(strict_types=1);

namespace NimVor\Tests\Integration;

use NimVor\Security\Permissions;
use NimVor\Services\RoleService;
use NimVor\Services\UserService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\Tests\Support\DatabaseTestCase;

final class RoleServiceTest extends DatabaseTestCase
{
    public function testCreateUpdateDeleteCustomRole(): void
    {
        $svc = new RoleService($this->roles, $this->audit);
        $id = $svc->create(['name' => 'support', 'label' => 'Support', 'permissions' => [Permissions::WEBSITES_VIEW, Permissions::DASHBOARD_VIEW]]);
        self::assertSame([Permissions::DASHBOARD_VIEW, Permissions::WEBSITES_VIEW], $this->roles->permissionsFor($id));

        $svc->update($id, ['label' => 'Support team', 'permissions' => [Permissions::DASHBOARD_VIEW]]);
        self::assertSame([Permissions::DASHBOARD_VIEW], $this->roles->permissionsFor($id));
        self::assertSame('Support team', $this->roles->find($id)['label']);

        $this->createUser('sam', 'support');
        try {
            $svc->delete($id);
            self::fail('role in use must not be deletable');
        } catch (UserFacingException) {
        }
        $this->db->run('DELETE FROM users');
        $svc->delete($id);
        self::assertNull($this->roles->find($id));
    }

    public function testRejectsUnknownPermissionsAndSystemRoleEdits(): void
    {
        $svc = new RoleService($this->roles, $this->audit);
        try {
            $svc->create(['name' => 'x_role', 'label' => 'X', 'permissions' => ['root.shell']]);
            self::fail('unknown permission accepted');
        } catch (ValidationException $e) {
            self::assertArrayHasKey('permissions', $e->errors());
        }
        $this->expectException(UserFacingException::class);
        $svc->update($this->roleId('admin'), ['label' => 'Pwned', 'permissions' => []]);
    }

    public function testCustomRoleSurvivesSync(): void
    {
        $svc = new RoleService($this->roles, $this->audit);
        $id = $svc->create(['name' => 'support', 'label' => 'Support', 'permissions' => [Permissions::WEBSITES_VIEW]]);
        $this->roles->syncDefaults();
        self::assertSame([Permissions::WEBSITES_VIEW], $this->roles->permissionsFor($id));
    }

    public function testCannotAssignRoleWithMorePermissionsThanOwn(): void
    {
        $roles = new RoleService($this->roles, $this->audit);
        $managerRole = $roles->create(['name' => 'user_manager', 'label' => 'User manager', 'permissions' => [Permissions::USERS_VIEW, Permissions::USERS_MANAGE]]);
        $manager = $this->users->create(['username' => 'mgr', 'email' => 'mgr@example.test', 'display_name' => '', 'password_hash' => 'x', 'role_id' => $managerRole, 'is_active' => true]);
        $users = new UserService($this->users, $this->roles, $this->hasher, $this->audit);

        $input = ['username' => 'eve', 'email' => 'eve@example.test', 'password' => 'a-long-enough-pass', 'password_confirmation' => 'a-long-enough-pass', 'is_active' => '1'];
        try {
            $users->create($input + ['role_id' => $this->roleId('admin')], $manager);
            self::fail('escalation to admin must be refused');
        } catch (ValidationException $e) {
            self::assertArrayHasKey('role_id', $e->errors());
        }
        // Assigning a role that is a subset of the manager's own is fine.
        $users->create($input + ['role_id' => $managerRole], $manager);
        try {
            $users->update($manager, ['email' => 'mgr@example.test', 'role_id' => $this->roleId('admin'), 'is_active' => true], $manager);
            self::fail('self-promotion must be refused');
        } catch (ValidationException $e) {
            self::assertArrayHasKey('role_id', $e->errors());
        }
    }

    public function testBuiltInRolesAreEditableAndKeepChangesOnSync(): void
    {
        $svc = new RoleService($this->roles, $this->audit);
        $op = $this->roleId('operator');
        $svc->update($op, ['label' => 'ignored', 'permissions' => [Permissions::DASHBOARD_VIEW, Permissions::SETTINGS_VIEW]]);
        self::assertSame([Permissions::DASHBOARD_VIEW, Permissions::SETTINGS_VIEW], $this->roles->permissionsFor($op));
        self::assertSame('Operator', $this->roles->find($op)['label'], 'built-in labels stay NimVor\'s');
        $this->roles->syncDefaults();
        self::assertSame([Permissions::DASHBOARD_VIEW, Permissions::SETTINGS_VIEW], $this->roles->permissionsFor($op), 'an upgrade keeps the change');
        self::assertContains(Permissions::WEBSITES_VIEW, $svc->missingDefaults($this->roles->find($op)));

        $svc->resetDefaults($op);
        self::assertContains(Permissions::WEBSITES_VIEW, $this->roles->permissionsFor($op));
        self::assertSame(0, (int) $this->roles->find($op)['customized']);
        self::assertSame([], $svc->missingDefaults($this->roles->find($op)));
    }

    public function testOwnScopeRolesTakeNoServerPermissions(): void
    {
        $svc = new RoleService($this->roles, $this->audit);
        $customer = $this->roleId('customer');
        try {
            $svc->update($customer, ['permissions' => array_merge($this->roles->permissionsFor($customer), [Permissions::SERVER_MANAGE])]);
            self::fail('a customer role must not get server.manage');
        } catch (ValidationException $e) {
            self::assertStringContainsString('server.manage', $e->errors()['permissions']);
        }
        self::assertNotContains(Permissions::SERVER_MANAGE, $this->roles->permissionsFor($customer));
        // Hosting permissions can be taken away.
        $svc->update($customer, ['permissions' => [Permissions::DASHBOARD_VIEW, Permissions::WEBSITES_VIEW]]);
        self::assertSame([Permissions::DASHBOARD_VIEW, Permissions::WEBSITES_VIEW], $this->roles->permissionsFor($customer));
    }

    public function testCannotGrantPermissionsOneDoesNotHold(): void
    {
        $svc = new RoleService($this->roles, $this->audit);
        $editorRole = $svc->create(['name' => 'role_editor', 'label' => 'Role editor', 'permissions' => [Permissions::ROLES_MANAGE, Permissions::USERS_VIEW, Permissions::DASHBOARD_VIEW]]);
        $editor = ['id' => 99, 'role_id' => $editorRole];
        try {
            $svc->update($editorRole, ['label' => 'Role editor', 'permissions' => [Permissions::ROLES_MANAGE, Permissions::USERS_VIEW, Permissions::DASHBOARD_VIEW, Permissions::SECURITY_MANAGE]], $editor);
            self::fail('self-escalation through the own role must be refused');
        } catch (ValidationException $e) {
            self::assertStringContainsString('security.manage', $e->errors()['permissions']);
        }
        try {
            $svc->create(['name' => 'sneaky', 'label' => 'S', 'permissions' => [Permissions::SERVER_MANAGE]], $editor);
            self::fail('a new role beyond the editor\'s permissions must be refused');
        } catch (ValidationException) {
        }
        // Granting what one has, and keeping grants one lacks, is fine.
        $viewer = $this->roleId('viewer');
        $before = $this->roles->permissionsFor($viewer);
        $svc->update($viewer, ['permissions' => $before], $editor);
        self::assertSame($before, $this->roles->permissionsFor($viewer));
    }
}
