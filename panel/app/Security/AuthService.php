<?php

declare(strict_types=1);

namespace WhaCP\Security;

use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\AuditLogger;

/**
 * Session authentication.
 *
 * The session stores the user ID plus a fingerprint of the password hash, so
 * changing a password (or disabling a user) invalidates all other sessions.
 */
final class AuthService
{
    private const USER_KEY = '_auth_user_id';
    private const FINGERPRINT_KEY = '_auth_fp';
    private const LOGIN_AT_KEY = '_auth_login_at';
    private const ACTIVITY_KEY = '_auth_last_activity';

    /** A real Argon2id hash of a random, discarded value (same cost as user hashes). */
    private const DUMMY_HASH = '$argon2id$v=19$m=65536,t=4,p=1$dVkvM1B6dzNvTFI1bW40Rg$4ObwPqsBPsJMDj4LO19/WrcHGZflXqksE8+8FUXeSAc';

    /** @var array<string, mixed>|null|false false = not loaded yet */
    private array|null|false $user = false;

    /** @var callable(): int */
    private $clock;

    public function __construct(
        private readonly SessionInterface $session,
        private readonly UserRepository $users,
        private readonly PasswordHasher $hasher,
        private readonly LoginThrottle $throttle,
        private readonly AuditLogger $audit,
        private readonly int $idleTimeout = 1800,
        private readonly int $absoluteTimeout = 43200,
        ?callable $clock = null,
    ) {
        $this->clock = $clock ?? time(...);
    }

    /**
     * Attempt a login. Returns a user-facing error message, or null on success.
     */
    public function attempt(string $username, string $password, string $ip): ?string
    {
        $username = trim($username);
        $retry = $this->throttle->retryAfter($username, $ip);
        if ($retry > 0) {
            $this->audit->record('auth.login', 'user', $username, AuditLogger::DENIED, ['reason' => 'throttled'], null, $username);
            return sprintf('Too many failed attempts. Try again in %d minute(s).', (int) ceil($retry / 60));
        }

        $row = $username === '' ? null : $this->users->findForAuth($username);
        // Verify against a dummy hash when the user does not exist so timing
        // does not reveal which usernames are valid.
        $hash = $row !== null ? (string) $row['password_hash'] : self::DUMMY_HASH;
        $valid = $this->hasher->verify($password, $hash);

        if ($row === null || !$valid || (int) $row['is_active'] !== 1) {
            $this->throttle->recordFailure($username, $ip);
            $reason = $row === null ? 'unknown_user' : (!$valid ? 'bad_password' : 'inactive');
            $this->audit->record('auth.login', 'user', $username, AuditLogger::FAILURE, ['reason' => $reason], $row === null ? null : (int) $row['id'], $username);
            return 'Invalid username or password.';
        }

        $id = (int) $row['id'];
        if ($this->hasher->needsRehash($hash)) {
            $hash = $this->hasher->hash($password);
            $this->users->updatePassword($id, $hash);
        }
        $this->throttle->recordSuccess($username, $ip);
        $this->users->recordLogin($id, $ip);

        $this->session->regenerate();
        $now = ($this->clock)();
        $this->session->set(self::USER_KEY, $id);
        $this->session->set(self::FINGERPRINT_KEY, self::fingerprint($hash));
        $this->session->set(self::LOGIN_AT_KEY, $now);
        $this->session->set(self::ACTIVITY_KEY, $now);
        $this->user = false;

        $this->audit->record('auth.login', 'user', (string) $id, AuditLogger::SUCCESS, [], $id, (string) $row['username']);
        return null;
    }

    public function logout(): void
    {
        $user = $this->user();
        if ($user !== null) {
            $this->audit->record('auth.logout', 'user', (string) $user['id'], AuditLogger::SUCCESS, [], (int) $user['id'], (string) $user['username']);
        }
        $this->session->invalidate();
        $this->user = null;
    }

    /**
     * The authenticated user, or null. Enforces idle and absolute timeouts
     * and password-change invalidation.
     *
     * @return array<string, mixed>|null
     */
    public function user(): ?array
    {
        if ($this->user !== false) {
            return $this->user;
        }
        $this->user = null;
        $id = $this->session->get(self::USER_KEY);
        if (!is_int($id)) {
            return null;
        }
        $now = ($this->clock)();
        $loginAt = (int) $this->session->get(self::LOGIN_AT_KEY, 0);
        $activity = (int) $this->session->get(self::ACTIVITY_KEY, 0);
        if ($now - $activity > $this->idleTimeout || $now - $loginAt > $this->absoluteTimeout) {
            $this->session->invalidate();
            return null;
        }
        $user = $this->users->find($id);
        $hash = $this->users->passwordHash($id);
        if ($user === null || $hash === null || (int) $user['is_active'] !== 1
            || !hash_equals((string) $this->session->get(self::FINGERPRINT_KEY, ''), self::fingerprint($hash))) {
            $this->session->invalidate();
            return null;
        }
        $this->session->set(self::ACTIVITY_KEY, $now);
        return $this->user = $user;
    }

    /** Refresh the session fingerprint after the current user changes their own password. */
    public function refreshFingerprint(int $userId): void
    {
        $hash = $this->users->passwordHash($userId);
        if ($hash !== null && $this->session->get(self::USER_KEY) === $userId) {
            $this->session->regenerate();
            $this->session->set(self::FINGERPRINT_KEY, self::fingerprint($hash));
            $this->user = false;
        }
    }

    private static function fingerprint(string $hash): string
    {
        return hash('sha256', 'whacp-session|' . $hash);
    }
}
