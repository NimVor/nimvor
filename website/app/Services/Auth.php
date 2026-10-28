<?php

declare(strict_types=1);

namespace NimVorSite\Services;

use NimVorSite\Core\Session;
use NimVorSite\Repositories\Users;

final class Auth
{
    private const FAIL_LIMIT_IP = 20;
    private const FAIL_LIMIT_ACCOUNT = 6;
    private const WINDOW = 900;

    /** @var array<string, mixed>|null */
    private ?array $user = null;
    private bool $loaded = false;

    public function __construct(private readonly Users $users, private readonly Session $session, private readonly RateLimiter $limiter)
    {
    }

    /** @return array<string, mixed>|null the signed-in, active user */
    public function user(): ?array
    {
        if (!$this->loaded) {
            $this->loaded = true;
            $id = $this->session->get('user_id');
            $u = is_int($id) ? $this->users->find($id) : null;
            $this->user = $u !== null && (int) $u['is_active'] === 1 ? $u : null;
            if ($u !== null && $this->user === null) {
                $this->session->destroy();
            }
        }
        return $this->user;
    }

    public function isAdmin(): bool
    {
        return ($this->user()['role'] ?? '') === 'admin';
    }

    /**
     * @return array{user: array<string, mixed>|null, error: ?string} error is "throttled" or "invalid"
     *         ("invalid" never says whether the email exists)
     */
    public function attempt(string $email, string $password, string $ip): array
    {
        $ipKey = 'login:ip:' . $ip;
        $acctKey = 'login:acct:' . hash('sha256', strtolower($email));
        if ($this->limiter->blocked($ipKey, self::FAIL_LIMIT_IP) || $this->limiter->blocked($acctKey, self::FAIL_LIMIT_ACCOUNT)) {
            return ['user' => null, 'error' => 'throttled'];
        }
        $u = $this->users->byEmail($email);
        // Always one hash check, so a missing account takes as long as a wrong password.
        $hash = $u['password_hash'] ?? self::dummyHash();
        $ok = password_verify($password, $hash) && $u !== null && (int) $u['is_active'] === 1;
        if (!$ok) {
            $this->limiter->hit($ipKey, self::FAIL_LIMIT_IP, self::WINDOW);
            $this->limiter->hit($acctKey, self::FAIL_LIMIT_ACCOUNT, self::WINDOW);
            return ['user' => null, 'error' => 'invalid'];
        }
        $this->limiter->clear($acctKey);
        if (password_needs_rehash($u['password_hash'], PASSWORD_ARGON2ID)) {
            $this->users->setPassword((int) $u['id'], self::hash($password));
        }
        return ['user' => $u, 'error' => null];
    }

    /** @param array<string, mixed> $user */
    public function login(array $user): void
    {
        $this->session->regenerate();
        $this->session->put('user_id', (int) $user['id']);
        $this->session->put('csrf', bin2hex(random_bytes(32)));
        $this->users->touchLogin((int) $user['id']);
        $this->user = $user;
        $this->loaded = true;
    }

    public function logout(): void
    {
        $this->session->destroy();
        $this->user = null;
        $this->loaded = true;
    }

    private static function dummyHash(): string
    {
        static $h = null;
        return $h ??= password_hash(bin2hex(random_bytes(16)), PASSWORD_ARGON2ID);
    }

    public static function hash(string $password): string
    {
        return password_hash($password, PASSWORD_ARGON2ID);
    }
}
