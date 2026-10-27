<?php

declare(strict_types=1);

namespace NimVor\Security;

use NimVor\Repositories\RoleRepository;

/**
 * Role-based access control checks.
 */
final class Authorizer
{
    /** @var array<int, list<string>> */
    private array $cache = [];

    public function __construct(
        private readonly RoleRepository $roles,
        private readonly ?\NimVor\Services\FeatureService $features = null,
    ) {
    }

    /** @param array<string, mixed>|null $user */
    public function can(?array $user, string $permission): bool
    {
        if (!\NimVor\Repositories\UserRepository::canAct($user)) {
            return false;
        }
        if (!in_array($permission, $this->permissions((int) $user['role_id']), true)) {
            return false;
        }
        // Features the account's plan turns off withhold their permissions.
        return $this->features === null || $this->features->permissionAllowed($user, $permission);
    }

    /**
     * Whether a route of a feature the account's plan turns off (features
     * that share a permission with others are matched by route name).
     *
     * @param array<string, mixed>|null $user
     */
    public function routeAllowed(?array $user, ?string $route): bool
    {
        return $user === null || $this->features === null || $this->features->routeAllowed($user, $route);
    }

    /** @return list<string> */
    public function permissions(int $roleId): array
    {
        return $this->cache[$roleId] ??= $this->roles->permissionsFor($roleId);
    }
}
