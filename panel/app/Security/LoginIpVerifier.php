<?php

declare(strict_types=1);

namespace WhaCP\Security;

use WhaCP\Database\Connection;
use WhaCP\Mail\Mailer;
use WhaCP\Mail\MailException;
use WhaCP\Services\SettingsService;

/**
 * Security policy "Limit logins to verified IP addresses": a sign-in from
 * an IP address the user has not verified needs a one-time code sent to the
 * user's email address. Verified addresses are kept per user.
 */
final class LoginIpVerifier
{
    public const CODE_TTL = 900;

    public function __construct(
        private readonly Connection $db,
        private readonly Mailer $mailer,
        private readonly SettingsService $settings,
    ) {
    }

    public function enabled(): bool
    {
        return $this->settings->bool('policy_verified_ips');
    }

    /** Whether a sign-in of this user from this address needs a code. */
    public function required(int $userId, string $ip): bool
    {
        return $this->enabled() && !$this->verified($userId, $ip);
    }

    public function verified(int $userId, string $ip): bool
    {
        return $this->db->scalar('SELECT id FROM login_ips WHERE user_id = ? AND ip = ?', [$userId, $ip]) !== null;
    }

    public function remember(int $userId, string $ip): void
    {
        $now = gmdate('c');
        $this->db->run('INSERT INTO login_ips (user_id, ip, verified_at, last_used_at) VALUES (?, ?, ?, ?) ON CONFLICT (user_id, ip) DO UPDATE SET last_used_at = excluded.last_used_at', [$userId, $ip, $now, $now]);
    }

    /** Record the address every user last signed in from (when the policy is turned on). */
    public function seedFromLastLogins(): int
    {
        $n = 0;
        foreach ($this->db->fetchAll("SELECT id, last_login_ip FROM users WHERE last_login_ip IS NOT NULL AND last_login_ip <> ''") as $u) {
            $this->remember((int) $u['id'], (string) $u['last_login_ip']);
            $n++;
        }
        return $n;
    }

    /** @return list<array<string, mixed>> */
    public function list(int $userId): array
    {
        return $this->db->fetchAll('SELECT id, ip, verified_at, last_used_at FROM login_ips WHERE user_id = ? ORDER BY COALESCE(last_used_at, verified_at) DESC', [$userId]);
    }

    public function forget(int $userId, int $id): void
    {
        $this->db->run('DELETE FROM login_ips WHERE user_id = ? AND id = ?', [$userId, $id]);
    }

    /**
     * Email a new code. Returns the code's hash to keep in the session, or
     * throws with a user-facing reason.
     *
     * @param array<string, mixed> $user
     */
    public function sendCode(array $user, string $ip): string
    {
        $email = (string) ($user['email'] ?? '');
        if ($email === '' || !$this->mailer->configured()) {
            throw new \RuntimeException('Sign-ins from new IP addresses must be verified by email, but ' . ($email === '' ? 'your account has no email address' : 'the panel cannot send email') . '. Ask your administrator to verify this address for you.');
        }
        $code = (string) random_int(100000, 999999);
        $name = $this->settings->string('panel_name');
        try {
            $this->mailer->send([$email], "{$name}: verify a sign-in from a new IP address", implode("\n", [
                "Someone (hopefully you) signed in to the {$name} account \"{$user['username']}\" from the IP address {$ip}, which has not been used before.",
                '',
                "To allow it, enter this code within 15 minutes: {$code}",
                '',
                'If this was not you, do not enter the code and change your password.',
            ]));
        } catch (MailException) {
            throw new \RuntimeException('The verification email could not be sent. Try again later or ask your administrator to verify this address.');
        }
        return self::hash($code);
    }

    public static function hash(string $code): string
    {
        return hash('sha256', 'whacp-login-ip|' . $code);
    }
}
