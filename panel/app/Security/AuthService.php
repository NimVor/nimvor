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
    private const PENDING_KEY = '_auth_2fa_pending';
    /** Seconds allowed between the password and the second factor. */
    private const PENDING_TTL = 300;
    private const PENDING_TRIES = 5;

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
        private readonly ?TwoFactor $twoFactor = null,
        private readonly ?WebAuthn $webAuthn = null,
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
        if ($this->hasSecondFactor($id)) {
            // Password is right; the session is not signed in until the
            // second factor is given.
            $this->session->regenerate();
            $this->session->set(self::PENDING_KEY, ['id' => $id, 'username' => $username, 'fp' => self::fingerprint($hash), 'at' => ($this->clock)(), 'tries' => 0]);
            $this->audit->record('auth.login', 'user', (string) $id, AuditLogger::SUCCESS, ['step' => 'password', 'two_factor' => 'required'], $id, (string) $row['username']);
            return null;
        }
        $this->finishLogin($row, $hash, $username, $ip, []);
        return null;
    }

    /** @param array<string, mixed> $row @param array<string, mixed> $details */
    private function finishLogin(array $row, string $hash, string $username, string $ip, array $details): void
    {
        $id = (int) $row['id'];
        $this->throttle->recordSuccess($username, $ip);
        $this->users->recordLogin($id, $ip);

        $this->session->regenerate();
        $this->session->remove(self::PENDING_KEY);
        $now = ($this->clock)();
        $this->session->set(self::USER_KEY, $id);
        $this->session->set(self::FINGERPRINT_KEY, self::fingerprint($hash));
        $this->session->set(self::LOGIN_AT_KEY, $now);
        $this->session->set(self::ACTIVITY_KEY, $now);
        $this->user = false;

        $this->audit->record('auth.login', 'user', (string) $id, AuditLogger::SUCCESS, $details, $id, (string) $row['username']);
    }

    /** Whether a sign-in is waiting for its second factor. */
    public function pendingTwoFactor(): bool
    {
        $p = $this->session->get(self::PENDING_KEY);
        if (!is_array($p)) {
            return false;
        }
        if (($this->clock)() - (int) $p['at'] > self::PENDING_TTL) {
            $this->session->remove(self::PENDING_KEY);
            return false;
        }
        return true;
    }

    /** Whether the user signs in with a second factor (app code or security key). */
    public function hasSecondFactor(int $userId): bool
    {
        return ($this->twoFactor?->enabled($userId) ?? false) || ($this->webAuthn?->has($userId) ?? false);
    }

    /** @return array{totp: bool, key: bool} the second factors of the pending sign-in */
    public function pendingMethods(): array
    {
        $p = $this->pendingTwoFactor() ? (array) $this->session->get(self::PENDING_KEY) : [];
        $id = (int) ($p['id'] ?? 0);
        return ['totp' => $id > 0 && ($this->twoFactor?->enabled($id) ?? false), 'key' => $id > 0 && ($this->webAuthn?->has($id) ?? false)];
    }

    /**
     * The pending sign-in, checked: [pending, user row, password hash], or
     * an error message.
     *
     * @return array{0: array{id: int, username: string, fp: string, at: int, tries: int}, 1: array<string, mixed>, 2: string}|string
     */
    private function pending(string $ip): array|string
    {
        if (!$this->pendingTwoFactor()) {
            return 'Your sign-in expired. Enter your password again.';
        }
        /** @var array{id: int, username: string, fp: string, at: int, tries: int} $p */
        $p = $this->session->get(self::PENDING_KEY);
        $retry = $this->throttle->retryAfter($p['username'], $ip);
        if ($retry > 0) {
            $this->session->remove(self::PENDING_KEY);
            return sprintf('Too many failed attempts. Try again in %d minute(s).', (int) ceil($retry / 60));
        }
        $id = (int) $p['id'];
        $user = $this->users->find($id);
        $hash = $this->users->passwordHash($id);
        if ($user === null || $hash === null || (int) $user['is_active'] !== 1 || !hash_equals($p['fp'], self::fingerprint($hash))) {
            $this->session->remove(self::PENDING_KEY);
            return 'Your sign-in expired. Enter your password again.';
        }
        return [$p, $user, $hash];
    }

    /** @param array{id: int, username: string, fp: string, at: int, tries: int} $p @param array<string, mixed> $user */
    private function secondFactorFailed(array $p, array $user, string $ip, string $reason, string $message): string
    {
        $this->throttle->recordFailure($p['username'], $ip);
        $this->audit->record('auth.login', 'user', (string) $p['id'], AuditLogger::FAILURE, ['reason' => $reason], (int) $p['id'], (string) $user['username']);
        if (++$p['tries'] >= self::PENDING_TRIES) {
            $this->session->remove(self::PENDING_KEY);
            return 'Too many failed attempts. Enter your password again.';
        }
        $this->session->set(self::PENDING_KEY, $p);
        return $message;
    }

    /**
     * Complete a sign-in with an authenticator or recovery code. Returns a
     * user-facing error, or null when signed in.
     */
    public function completeTwoFactor(string $code, string $ip): ?string
    {
        if ($this->twoFactor === null) {
            return 'Your sign-in expired. Enter your password again.';
        }
        $pending = $this->pending($ip);
        if (is_string($pending)) {
            return $pending;
        }
        [$p, $user, $hash] = $pending;
        $method = $this->twoFactor->verify((int) $p['id'], $code);
        if ($method === null) {
            return $this->secondFactorFailed($p, $user, $ip, 'bad_two_factor_code', 'That code is not valid. Check the time on your phone and try again.');
        }
        $this->finishLogin($user, $hash, $p['username'], $ip, ['two_factor' => $method]);
        return null;
    }

    /**
     * Options for the browser to ask the pending user's security key.
     *
     * @return array<string, mixed>|null
     */
    public function securityKeyOptions(string $rpId): ?array
    {
        $p = $this->pendingTwoFactor() ? (array) $this->session->get(self::PENDING_KEY) : null;
        if ($p === null || $this->webAuthn === null || !$this->webAuthn->has((int) $p['id'])) {
            return null;
        }
        return $this->webAuthn->loginOptions((int) $p['id'], $rpId);
    }

    /**
     * Complete a sign-in with a security key assertion.
     *
     * @param array<string, mixed> $a id, client_data, authenticator_data, signature (base64url)
     */
    public function completeWithSecurityKey(string $origin, string $rpId, array $a, string $ip): ?string
    {
        if ($this->webAuthn === null) {
            return 'Security keys are not available.';
        }
        $pending = $this->pending($ip);
        if (is_string($pending)) {
            return $pending;
        }
        [$p, $user, $hash] = $pending;
        $str = static fn (string $k): string => is_string($a[$k] ?? null) ? substr($a[$k], 0, 8192) : '';
        try {
            $name = $this->webAuthn->verify((int) $p['id'], $origin, $rpId, $str('id'), $str('client_data'), $str('authenticator_data'), $str('signature'));
        } catch (WebAuthnException $e) {
            return $this->secondFactorFailed($p, $user, $ip, 'bad_security_key', $e->getMessage());
        }
        $this->finishLogin($user, $hash, $p['username'], $ip, ['two_factor' => 'security_key', 'key' => $name]);
        return null;
    }

    public function cancelTwoFactor(): void
    {
        $this->session->remove(self::PENDING_KEY);
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

    /** Whether this request counts as activity (see passive()). */
    private bool $passive = false;

    /**
     * This request is automatic (e.g. the notification count refreshing in
     * an open tab): it does not count as activity, so the idle timeout still
     * signs out an unattended browser. Call before user().
     */
    public function passive(): void
    {
        $this->passive = true;
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
        if (!$this->passive) {
            $this->session->set(self::ACTIVITY_KEY, $now);
        }
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
