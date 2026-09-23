<?php

declare(strict_types=1);

namespace WhaCP\Security;

final class PasswordHasher
{
    public const MIN_LENGTH = 12;
    public const MAX_LENGTH = 1024;

    public function hash(string $password): string
    {
        return password_hash($password, $this->algorithm());
    }

    public function verify(string $password, string $hash): bool
    {
        return password_verify($password, $hash);
    }

    public function needsRehash(string $hash): bool
    {
        return password_needs_rehash($hash, $this->algorithm());
    }

    /**
     * Returns a user-facing problem with the password, or null if acceptable.
     */
    public function weakness(string $password, string $username = ''): ?string
    {
        $len = mb_strlen($password);
        if ($len < self::MIN_LENGTH) {
            return 'The password must be at least ' . self::MIN_LENGTH . ' characters long.';
        }
        if ($len > self::MAX_LENGTH) {
            return 'The password is too long.';
        }
        if ($username !== '' && stripos($password, $username) !== false) {
            return 'The password must not contain the username.';
        }
        if (count(array_unique(mb_str_split($password))) < 5) {
            return 'The password is too repetitive.';
        }
        return null;
    }

    private function algorithm(): string
    {
        return defined('PASSWORD_ARGON2ID') ? PASSWORD_ARGON2ID : PASSWORD_BCRYPT;
    }
}
