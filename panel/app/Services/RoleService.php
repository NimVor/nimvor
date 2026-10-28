<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Repositories\RoleRepository;
use NimVor\Security\Permissions;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * Roles. Built-in roles (operator, viewer, reseller, customer) come with
 * NimVor's defaults; an administrator may change their permissions, after
 * which upgrades keep the change (until "Reset to NimVor defaults").
 * Administrator always has every permission, so nobody can weaken it or
 * lock everyone out. Nobody can grant a permission they do not hold, and
 * own-scope roles only take hosting permissions.
 */
final class RoleService
{
    public function __construct(
        private readonly RoleRepository $roles,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * @param array<string, mixed> $input
     * @param array<string, mixed>|null $actor who makes the change (null: the system)
     */
    public function create(array $input, ?array $actor = null): int
    {
        [$name, $label, $description, $perms] = $this->validate($input, true);
        $this->checkGrantable($perms, $actor, 'all');
        $id = $this->roles->create($name, $label, $description);
        $this->roles->setPermissions($id, $perms);
        $this->audit->record('role.create', 'role', (string) $id, AuditLogger::SUCCESS, ['name' => $name, 'permissions' => $perms]);
        return $id;
    }

    /**
     * @param array<string, mixed> $input
     * @param array<string, mixed>|null $actor who makes the change (null: the system)
     */
    public function update(int $id, array $input, ?array $actor = null): void
    {
        $role = $this->roles->find($id) ?? throw new UserFacingException('The role no longer exists.', status: 404);
        $system = (int) $role['is_system'] === 1;
        if ($system && (Permissions::defaultRoles()[$role['name']]['permissions'] ?? []) === ['*']) {
            throw new UserFacingException('The Administrator role cannot be changed.', 'It always has every permission, so the server can never be left without a full administrator.', 'Create a custom role, or change Operator, for narrower access.');
        }
        // Built-in roles keep their name, label and description from NimVor.
        if ($system) {
            $input['label'] = $role['label'];
            $input['description'] = $role['description'];
        }
        [, $label, $description, $perms] = $this->validate($input + ['name' => $role['name']], false);
        $before = $this->roles->permissionsFor($id);
        // Only what changes is checked against the actor's own permissions:
        // grants the role already has may stay.
        $this->checkGrantable(array_values(array_diff($perms, $before)), $actor, (string) ($role['scope'] ?? 'all'));
        if ($system) {
            $this->roles->setCustomized($id, true);
        } else {
            $this->roles->update($id, $label, $description);
        }
        $this->roles->setPermissions($id, $perms);
        $this->audit->record('role.update', 'role', (string) $id, AuditLogger::SUCCESS, [
            'name' => $role['name'],
            'granted' => array_values(array_diff($perms, $before)),
            'revoked' => array_values(array_diff($before, $perms)),
        ]);
    }

    /** A built-in role back to NimVor's default permissions. */
    public function resetDefaults(int $id): void
    {
        $role = $this->roles->find($id) ?? throw new UserFacingException('The role no longer exists.', status: 404);
        if ((int) $role['is_system'] !== 1) {
            throw new UserFacingException('Only built-in roles have NimVor defaults.');
        }
        $this->roles->setCustomized($id, false);
        $this->roles->syncDefaults();
        $this->audit->record('role.reset', 'role', (string) $id, AuditLogger::SUCCESS, ['name' => $role['name']]);
    }

    /**
     * Default permissions of a built-in role that it does not have now
     * (e.g. added by an upgrade after the role was customised).
     *
     * @return list<string>
     */
    public function missingDefaults(array $role): array
    {
        $defaults = Permissions::defaultRoles()[$role['name']]['permissions'] ?? [];
        if ((int) $role['is_system'] !== 1 || $defaults === ['*']) {
            return [];
        }
        return array_values(array_diff($defaults, $this->roles->permissionsFor((int) $role['id'])));
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
    /** Built-in roles too: this is a sign-in policy, not a permission change. */
    public function setRequireTwoFactor(int $id, bool $required): void
    {
        $role = $this->roles->find($id) ?? throw new UserFacingException('The role no longer exists.', status: 404);
        $this->roles->setRequireTwoFactor($id, $required);
        $this->audit->record('role.two_factor_policy', 'role', (string) $id, AuditLogger::SUCCESS, ['role' => $role['name'], 'required' => $required]);
    }

    /**
     * @param list<string> $perms permissions being granted
     * @param array<string, mixed>|null $actor
     */
    private function checkGrantable(array $perms, ?array $actor, string $scope): void
    {
        if ($scope === 'own') {
            $outside = array_diff($perms, Permissions::ownScopeAllowed());
            if ($outside !== []) {
                throw new ValidationException(['permissions' => 'Resellers and hosting customers only see their own objects; server-wide permissions cannot be given to them: ' . implode(', ', $outside)]);
            }
        }
        if ($actor !== null) {
            $own = $this->roles->permissionsFor((int) ($actor['role_id'] ?? 0));
            $beyond = array_diff($perms, $own);
            if ($beyond !== []) {
                throw new ValidationException(['permissions' => 'You cannot grant permissions you do not have yourself: ' . implode(', ', $beyond)]);
            }
        }
    }

    private function editable(int $id): array
    {
        $role = $this->roles->find($id) ?? throw new UserFacingException('The role no longer exists.', status: 404);
        if ((int) $role['is_system'] === 1) {
            throw new UserFacingException('Built-in roles cannot be changed.', 'They are defined by NimVor and updated with it.', 'Create a custom role instead.');
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
