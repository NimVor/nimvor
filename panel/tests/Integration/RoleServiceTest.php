<?php

declare(strict_types=1);

namespace WhaCP\Tests\Integration;

use WhaCP\Security\Permissions;
use WhaCP\Services\RoleService;
use WhaCP\Services\UserService;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\Tests\Support\DatabaseTestCase;

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
}
