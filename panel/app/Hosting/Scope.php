<?php

declare(strict_types=1);

namespace WhaCP\Hosting;

/**
 * Row-level visibility for hosting objects.
 *
 * Roles with scope "all" see everything on the server. Roles with scope
 * "own" (resellers, customers) see objects they own and, for resellers,
 * objects owned by their customers (users whose parent_id is the reseller).
 */
final class Scope
{
    /**
     * @param array<string, mixed> $user
     * @return array{0: string, 1: array<string, mixed>} SQL condition and params
     */
    public static function condition(array $user, string $ownerColumn = 'owner_id'): array
    {
        if (($user['role_scope'] ?? 'all') === 'all') {
            return ['1 = 1', []];
        }
        $id = (int) ($user['id'] ?? 0);
        return [
            "({$ownerColumn} = :scope_uid OR {$ownerColumn} IN (SELECT id FROM users WHERE parent_id = :scope_uid))",
            ['scope_uid' => $id],
        ];
    }

    /** @param array<string, mixed> $user */
    public static function isAll(array $user): bool
    {
        return ($user['role_scope'] ?? 'all') === 'all';
    }
}
