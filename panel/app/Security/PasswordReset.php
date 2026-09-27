<?php

declare(strict_types=1);

namespace WhaCP\Security;

use WhaCP\Database\Connection;
use WhaCP\Mail\Mailer;
use WhaCP\Mail\MailException;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\SettingsService;
use WhaCP\Support\Logger;
use WhaCP\Support\ValidationException;

/**
 * "Forgot password" by email.
 *
 * Requests always get the same answer, whether or not the account exists.
 * Tokens are 256-bit, stored only as SHA-256 hashes, valid for 30 minutes,
 * single-use, and all of a user's tokens die when one is used. Links are
 * built from the configured public panel URL, never from the request's Host
 * header. Two-factor sign-in, when on, is still required afterwards.
 */
final class PasswordReset
{
    public const TTL = 1800;
    private const PER_USER_HOUR = 3;
    private const PER_IP_HOUR = 10;

    /** @var callable(): int */
    private $clock;

    public function __construct(
        private readonly Connection $db,
        private readonly UserRepository $users,
        private readonly PasswordHasher $hasher,
        private readonly Mailer $mailer,
        private readonly SettingsService $settings,
        private readonly AuditLogger $audit,
        private readonly Logger $log,
        ?callable $clock = null,
    ) {
        $this->clock = $clock ?? time(...);
    }

    public function available(): bool
    {
        return $this->mailer->configured() && $this->settings->string('panel_url') !== '';
    }

    public function request(string $identifier, string $ip): void
    {
        $now = ($this->clock)();
        $identifier = trim(mb_substr($identifier, 0, 190));
        if (!$this->available() || $identifier === '') {
            return;
        }
        $this->db->run('DELETE FROM password_resets WHERE created_at < ?', [$now - 86400]);
        $perIp = (int) $this->db->scalar('SELECT COUNT(*) FROM password_resets WHERE ip = ? AND created_at > ?', [$ip, $now - 3600]);
        $row = $this->users->findForAuth($identifier);
        // Every request counts against the IP, including unknown accounts.
        $this->db->run('INSERT INTO password_resets (user_id, token_hash, ip, created_at, expires_at) VALUES (?, NULL, ?, ?, ?)', [null, $ip, $now, $now]);
        if ($perIp >= self::PER_IP_HOUR) {
            $this->audit->record('auth.password_reset_request', 'user', $identifier, AuditLogger::DENIED, ['reason' => 'throttled_ip'], null, $identifier);
            return;
        }
        if (!\WhaCP\Repositories\UserRepository::canAct($row)) {
            $this->audit->record('auth.password_reset_request', 'user', $identifier, AuditLogger::FAILURE, ['reason' => $row === null ? 'unknown_user' : 'inactive'], null, $identifier);
            return;
        }
        $id = (int) $row['id'];
        $perUser = (int) $this->db->scalar('SELECT COUNT(*) FROM password_resets WHERE user_id = ? AND created_at > ?', [$id, $now - 3600]);
        if ($perUser >= self::PER_USER_HOUR) {
            $this->audit->record('auth.password_reset_request', 'user', (string) $id, AuditLogger::DENIED, ['reason' => 'throttled_user'], $id, (string) $row['username']);
            return;
        }
        $token = bin2hex(random_bytes(32));
        $this->db->run('INSERT INTO password_resets (user_id, token_hash, ip, created_at, expires_at) VALUES (?, ?, ?, ?, ?)', [$id, hash('sha256', $token), $ip, $now, $now + self::TTL]);
        $link = $this->settings->string('panel_url') . '/password/reset/' . $token;
        $name = $this->settings->string('panel_name');
        try {
            $this->mailer->send([(string) ($this->users->find($id)['email'] ?? '')], "{$name}: reset your password", implode("\n", [
                "Someone (hopefully you) asked to reset the password of the {$name} account \"{$row['username']}\".",
                '',
                'Choose a new password here within 30 minutes:',
                $link,
                '',
                "The request came from {$ip}. If it was not you, ignore this email; your password stays unchanged.",
            ]));
            $this->audit->record('auth.password_reset_request', 'user', (string) $id, AuditLogger::SUCCESS, [], $id, (string) $row['username']);
        } catch (MailException $e) {
            $this->log->error('password reset email failed', ['user' => $id, 'error' => $e->getMessage() . ' ' . $e->reason()]);
            $this->audit->record('auth.password_reset_request', 'user', (string) $id, AuditLogger::FAILURE, ['reason' => 'mail_failed'], $id, (string) $row['username']);
        }
    }

    /** @return array<string, mixed>|null the user, when the token is valid */
    public function userFor(string $token): ?array
    {
        if (!preg_match('/^[a-f0-9]{64}$/', $token)) {
            return null;
        }
        $row = $this->db->fetch(
            'SELECT user_id FROM password_resets WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?',
            [hash('sha256', $token), ($this->clock)()]
        );
        if ($row === null) {
            return null;
        }
        $user = $this->users->find((int) $row['user_id']);
        return \WhaCP\Repositories\UserRepository::canAct($user) ? $user : null;
    }

    public function reset(string $token, string $password, string $confirmation): void
    {
        $user = $this->userFor($token);
        if ($user === null) {
            throw new ValidationException(['token' => 'This link has expired or was already used. Ask for a new one.']);
        }
        if (($weak = $this->hasher->weakness($password, (string) $user['username'])) !== null) {
            throw new ValidationException(['password' => $weak]);
        }
        if (!hash_equals($password, $confirmation)) {
            throw new ValidationException(['password_confirmation' => 'The passwords do not match.']);
        }
        $id = (int) $user['id'];
        $this->db->transaction(function () use ($id, $password): void {
            $this->users->updatePassword($id, $this->hasher->hash($password));
            $this->db->run('UPDATE password_resets SET used_at = ? WHERE user_id = ? AND used_at IS NULL', [($this->clock)(), $id]);
        });
        $this->audit->record('user.password_reset', 'user', (string) $id, AuditLogger::SUCCESS, ['via' => 'email'], $id, (string) $user['username']);
    }
}
