<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Repositories\RoleRepository;
use WhaCP\Repositories\UserRepository;
use WhaCP\Security\PasswordHasher;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * Panel user management with validation and safety rules:
 * - the last active administrator can never be removed, disabled or demoted;
 * - users cannot delete or disable themselves.
 */
final class UserService
{
    public function __construct(
        private readonly UserRepository $users,
        private readonly RoleRepository $roles,
        private readonly PasswordHasher $hasher,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * @param array<string, mixed> $input
     */
    public function create(array $input, ?int $actorId = null): int
    {
        $data = $this->validate($input, null);
        $this->assertCanAssign($actorId, $data['role_id']);
        $id = $this->users->create([
            'username' => $data['username'],
            'email' => $data['email'],
            'display_name' => $data['display_name'],
            'password_hash' => $this->hasher->hash($data['password']),
            'role_id' => $data['role_id'],
            'is_active' => $data['is_active'],
        ]);
        $this->audit->record('user.create', 'user', (string) $id, AuditLogger::SUCCESS, [
            'username' => $data['username'], 'role_id' => $data['role_id'],
        ]);
        return $id;
    }

    /**
     * @param array<string, mixed> $input
     */
    public function update(int $id, array $input, int $actorId): void
    {
        $existing = $this->users->find($id) ?? throw new UserFacingException('The user no longer exists.', status: 404);
        $data = $this->validate($input + ['username' => $existing['username']], $id);
        if ($data['role_id'] !== (int) $existing['role_id']) {
            $this->assertCanAssign($actorId, $data['role_id']);
            $this->assertCanAssign($actorId, (int) $existing['role_id']);
        }

        if ($id === $actorId && !$data['is_active']) {
            throw new ValidationException(['is_active' => 'You cannot disable your own account.']);
        }
        $newRole = $this->roles->find($data['role_id']);
        $wasAdmin = $existing['role_name'] === 'admin' && (int) $existing['is_active'] === 1;
        $staysAdmin = ($newRole['name'] ?? '') === 'admin' && $data['is_active'];
        if ($wasAdmin && !$staysAdmin && $this->users->countActiveWithRole('admin') <= 1) {
            throw new ValidationException(['role_id' => 'At least one active administrator is required.']);
        }

        $this->users->update($id, [
            'email' => $data['email'],
            'display_name' => $data['display_name'],
            'role_id' => $data['role_id'],
            'is_active' => $data['is_active'],
        ]);
        if ($data['password'] !== '') {
            $this->users->updatePassword($id, $this->hasher->hash($data['password']));
        }
        $this->audit->record('user.update', 'user', (string) $id, AuditLogger::SUCCESS, [
            'username' => $existing['username'],
            'role_changed' => (int) $existing['role_id'] !== $data['role_id'],
            'active' => $data['is_active'],
            'password_changed' => $data['password'] !== '',
        ]);
    }

    public function delete(int $id, int $actorId): void
    {
        $existing = $this->users->find($id) ?? throw new UserFacingException('The user no longer exists.', status: 404);
        if ($id === $actorId) {
            throw new UserFacingException('You cannot delete your own account.', 'Deleting the account you are signed in with would lock you out.', 'Ask another administrator to remove it.');
        }
        if ($existing['role_name'] === 'admin' && (int) $existing['is_active'] === 1 && $this->users->countActiveWithRole('admin') <= 1) {
            throw new UserFacingException('The last administrator cannot be deleted.', 'WhaCP requires at least one active administrator.', 'Promote another user to Administrator first.');
        }
        $this->users->delete($id);
        $this->audit->record('user.delete', 'user', (string) $id, AuditLogger::SUCCESS, ['username' => $existing['username']]);
    }

    public function changeOwnPassword(int $id, string $current, string $new, string $confirm): void
    {
        $hash = $this->users->passwordHash($id);
        $user = $this->users->find($id);
        if ($hash === null || $user === null) {
            throw new UserFacingException('The user no longer exists.', status: 404);
        }
        if (!$this->hasher->verify($current, $hash)) {
            $this->audit->record('user.password_change', 'user', (string) $id, AuditLogger::FAILURE, ['reason' => 'bad_current_password']);
            throw new ValidationException(['current_password' => 'The current password is incorrect.']);
        }
        if ($new !== $confirm) {
            throw new ValidationException(['password_confirmation' => 'The passwords do not match.']);
        }
        if (($weak = $this->hasher->weakness($new, (string) $user['username'])) !== null) {
            throw new ValidationException(['password' => $weak]);
        }
        $this->users->updatePassword($id, $this->hasher->hash($new));
        $this->audit->record('user.password_change', 'user', (string) $id, AuditLogger::SUCCESS);
    }

    /**
     * A user may only hand out (or take away) a role whose permissions they
     * hold themselves, so nobody can escalate beyond their own rights.
     * The CLI (no actor) is trusted.
     */
    private function assertCanAssign(?int $actorId, int $roleId): void
    {
        if ($actorId === null) {
            return;
        }
        $actor = $this->users->find($actorId);
        $mine = $actor === null ? [] : $this->roles->permissionsFor((int) $actor['role_id']);
        $missing = array_diff($this->roles->permissionsFor($roleId), $mine);
        if ($missing !== []) {
            throw new ValidationException(['role_id' => 'You cannot assign a role with permissions you do not have yourself.']);
        }
    }

    /**
     * @param array<string, mixed> $input
     * @return array{username: string, email: string, display_name: string, password: string, role_id: int, is_active: bool}
     */
    private function validate(array $input, ?int $id): array
    {
        $errors = [];
        $username = trim((string) ($input['username'] ?? ''));
        $email = trim((string) ($input['email'] ?? ''));
        $display = trim((string) ($input['display_name'] ?? ''));
        $password = (string) ($input['password'] ?? '');
        $confirm = (string) ($input['password_confirmation'] ?? '');
        $roleId = filter_var($input['role_id'] ?? null, FILTER_VALIDATE_INT);
        $active = filter_var($input['is_active'] ?? false, FILTER_VALIDATE_BOOL);

        if ($id === null) {
            if (!preg_match('/^[a-z][a-z0-9._-]{2,31}$/', $username)) {
                $errors['username'] = 'Use 3–32 lowercase letters, digits, dots, dashes or underscores, starting with a letter.';
            } elseif ($this->users->usernameExists($username)) {
                $errors['username'] = 'This username is already taken.';
            }
        }
        if (!filter_var($email, FILTER_VALIDATE_EMAIL) || mb_strlen($email) > 190) {
            $errors['email'] = 'Enter a valid email address.';
        } elseif ($this->users->emailExists($email, $id)) {
            $errors['email'] = 'This email address is already in use.';
        }
        if (mb_strlen($display) > 100 || preg_match('/[\x00-\x1F\x7F]/u', $display)) {
            $errors['display_name'] = 'Use at most 100 printable characters.';
        }
        if ($roleId === false || $this->roles->find($roleId) === null) {
            $errors['role_id'] = 'Choose a valid role.';
        }
        if ($id === null || $password !== '') {
            if (($weak = $this->hasher->weakness($password, $username)) !== null) {
                $errors['password'] = $weak;
            } elseif ($password !== $confirm) {
                $errors['password_confirmation'] = 'The passwords do not match.';
            }
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return [
            'username' => $username,
            'email' => $email,
            'display_name' => $display,
            'password' => $password,
            'role_id' => (int) $roleId,
            'is_active' => (bool) $active,
        ];
    }
}
