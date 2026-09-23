<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Repositories\RoleRepository;
use WhaCP\Security\Permissions;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * Custom roles. System roles (admin, operator, viewer, …) are defined in
 * code and cannot be edited here, so an upgrade can never silently change
 * what a custom role grants, and nobody can weaken the administrator role.
 */
final class RoleService
{
    public function __construct(
        private readonly RoleRepository $roles,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @param array<string, mixed> $input */
    public function create(array $input): int
    {
        [$name, $label, $description, $perms] = $this->validate($input, true);
        $id = $this->roles->create($name, $label, $description);
        $this->roles->setPermissions($id, $perms);
        $this->audit->record('role.create', 'role', (string) $id, AuditLogger::SUCCESS, ['name' => $name, 'permissions' => $perms]);
        return $id;
    }

    /** @param array<string, mixed> $input */
    public function update(int $id, array $input): void
    {
        $role = $this->editable($id);
        [, $label, $description, $perms] = $this->validate($input + ['name' => $role['name']], false);
        $before = $this->roles->permissionsFor($id);
        $this->roles->update($id, $label, $description);
        $this->roles->setPermissions($id, $perms);
        $this->audit->record('role.update', 'role', (string) $id, AuditLogger::SUCCESS, [
            'name' => $role['name'],
            'granted' => array_values(array_diff($perms, $before)),
            'revoked' => array_values(array_diff($before, $perms)),
        ]);
    }

    public function delete(int $id): void
    {
        $role = $this->editable($id);
        if ($this->roles->userCount($id) > 0) {
            throw new UserFacingException('The role is still assigned to users.', 'Deleting it would leave those users without a role.', 'Move the users to another role first.');
        }
        $this->roles->delete($id);
        $this->audit->record('role.delete', 'role', (string) $id, AuditLogger::SUCCESS, ['name' => $role['name']]);
    }

    /** @return array<string, mixed> */
    private function editable(int $id): array
    {
        $role = $this->roles->find($id) ?? throw new UserFacingException('The role no longer exists.', status: 404);
        if ((int) $role['is_system'] === 1) {
            throw new UserFacingException('Built-in roles cannot be changed.', 'They are defined by WhaCP and updated with it.', 'Create a custom role instead.');
        }
        return $role;
    }

    /**
     * @param array<string, mixed> $input
     * @return array{0: string, 1: string, 2: string, 3: list<string>}
     */
    private function validate(array $input, bool $isNew): array
    {
        $errors = [];
        $name = trim((string) ($input['name'] ?? ''));
        $label = trim((string) ($input['label'] ?? ''));
        $description = trim((string) ($input['description'] ?? ''));
        $perms = array_values(array_unique(array_map('strval', (array) ($input['permissions'] ?? []))));

        if ($isNew) {
            if (!preg_match('/^[a-z][a-z0-9_]{2,31}$/', $name)) {
                $errors['name'] = 'Use 3–32 lowercase letters, digits or underscores, starting with a letter.';
            } elseif ($this->roles->findByName($name) !== null) {
                $errors['name'] = 'A role with this name already exists.';
            }
        }
        if ($label === '' || mb_strlen($label) > 60) {
            $errors['label'] = 'Enter a label of at most 60 characters.';
        }
        if (mb_strlen($description) > 255) {
            $errors['description'] = 'Use at most 255 characters.';
        }
        $unknown = array_diff($perms, array_keys(Permissions::all()));
        if ($unknown !== []) {
            $errors['permissions'] = 'Unknown permission(s): ' . implode(', ', $unknown);
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        sort($perms);
        return [$name, $label, $description, $perms];
    }
}
