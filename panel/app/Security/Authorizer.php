<?php

declare(strict_types=1);

namespace WhaCP\Security;

use WhaCP\Repositories\RoleRepository;

/**
 * Role-based access control checks.
 */
final class Authorizer
{
    /** @var array<int, list<string>> */
    private array $cache = [];

    public function __construct(private readonly RoleRepository $roles)
    {
    }

    /** @param array<string, mixed>|null $user */
    public function can(?array $user, string $permission): bool
    {
        if (!\WhaCP\Repositories\UserRepository::canAct($user)) {
            return false;
        }
        return in_array($permission, $this->permissions((int) $user['role_id']), true);
    }

    /** @return list<string> */
    public function permissions(int $roleId): array
    {
        return $this->cache[$roleId] ??= $this->roles->permissionsFor($roleId);
    }
}
