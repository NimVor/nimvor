<?php

declare(strict_types=1);

namespace NimVor\Hosting;

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

    /**
     * Like condition(), but an object attached to a website is also visible
     * to whoever may see that website (e.g. the DNS zone an administrator
     * created for a customer's website).
     *
     * @param array<string, mixed> $user
     * @return array{0: string, 1: array<string, mixed>}
     */
    public static function withWebsite(array $user, string $ownerColumn, string $websiteColumn): array
    {
        [$cond, $params] = self::condition($user, $ownerColumn);
        if ($cond === '1 = 1') {
            return [$cond, $params];
        }
        [$siteCond] = self::condition($user, 'owner_id');
        return ["({$cond} OR {$websiteColumn} IN (SELECT id FROM websites WHERE {$siteCond}))", $params];
    }

    /** @param array<string, mixed> $user */
    public static function isAll(array $user): bool
    {
        return ($user['role_scope'] ?? 'all') === 'all';
    }
}
