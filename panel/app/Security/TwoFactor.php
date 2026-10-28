<?php

declare(strict_types=1);

namespace NimVor\Security;

use NimVor\Database\Connection;

/**
 * Time-based one-time passwords (RFC 6238: SHA-1, 6 digits, 30 seconds),
 * the scheme every authenticator app supports, plus single-use recovery
 * codes.
 *
 * Secrets are stored encrypted with the panel key; recovery codes only as
 * SHA-256 hashes. A code is accepted for the previous, current and next
 * time step, and never twice (the last used step is remembered).
 */
final class TwoFactor
{
    public const PERIOD = 30;
    private const B32 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
    private const RECOVERY_CODES = 10;

    /** @var callable(): int */
    private $clock;

    public function __construct(
        private readonly Connection $db,
        private readonly Crypto $crypto,
        ?callable $clock = null,
    ) {
        $this->clock = $clock ?? time(...);
    }

    public static function newSecret(): string
    {
        return self::base32Encode(random_bytes(20));
    }

    /** otpauth:// URI understood by authenticator apps. */
    public static function uri(string $secret, string $account, string $issuer = 'NimVor'): string
    {
        return 'otpauth://totp/' . rawurlencode($issuer . ':' . $account)
            . '?secret=' . $secret . '&issuer=' . rawurlencode($issuer) . '&algorithm=SHA1&digits=6&period=' . self::PERIOD;
    }

    public static function code(string $secret, int $step): string
    {
        $key = self::base32Decode($secret);
        $mac = hash_hmac('sha1', pack('J', $step), $key, true);
        $offset = ord($mac[19]) & 0x0f;
        $bin = ((ord($mac[$offset]) & 0x7f) << 24) | (ord($mac[$offset + 1]) << 16) | (ord($mac[$offset + 2]) << 8) | ord($mac[$offset + 3]);
        return str_pad((string) ($bin % 1_000_000), 6, '0', STR_PAD_LEFT);
    }

    /**
     * The time step a code matches (previous, current or next), or null.
     */
    public function matchStep(string $secret, string $code, int $after = 0): ?int
    {
        $code = preg_replace('/\s+/', '', $code) ?? '';
        if (!preg_match('/^\d{6}$/', $code)) {
            return null;
        }
        $now = intdiv(($this->clock)(), self::PERIOD);
        foreach ([$now - 1, $now, $now + 1] as $step) {
            if ($step > $after && hash_equals(self::code($secret, $step), $code)) {
                return $step;
            }
        }
        return null;
    }

    public function enabled(int $userId): bool
    {
        return $this->db->scalar('SELECT COUNT(*) FROM user_two_factor WHERE user_id = ?', [$userId]) > 0;
    }

    /** @return array{enabled_at: string, recovery_left: int}|null */
    public function status(int $userId): ?array
    {
        $row = $this->db->fetch('SELECT recovery_codes, enabled_at FROM user_two_factor WHERE user_id = ?', [$userId]);
        if ($row === null) {
            return null;
        }
        return ['enabled_at' => (string) $row['enabled_at'], 'recovery_left' => count((array) json_decode((string) $row['recovery_codes'], true))];
    }

    /**
     * Turn two-factor sign-in on after the user proved their app works.
     *
     * @return list<string>|null recovery codes (shown once), or null if the code is wrong
     */
    public function enable(int $userId, string $secret, string $code): ?array
    {
        $step = $this->matchStep($secret, $code);
        if ($step === null) {
            return null;
        }
        [$codes, $hashes] = self::recoveryCodes();
        $this->db->run(
            'INSERT INTO user_two_factor (user_id, secret, recovery_codes, last_step, enabled_at) VALUES (?, ?, ?, ?, ?)
             ON CONFLICT(user_id) DO UPDATE SET secret = excluded.secret, recovery_codes = excluded.recovery_codes, last_step = excluded.last_step, enabled_at = excluded.enabled_at',
            [$userId, $this->crypto->encrypt($secret), json_encode($hashes), $step, gmdate('c', ($this->clock)())]
        );
        return $codes;
    }

    public function disable(int $userId): void
    {
        $this->db->run('DELETE FROM user_two_factor WHERE user_id = ?', [$userId]);
    }

    /** @return list<string> new recovery codes; the old ones stop working */
    public function regenerateRecovery(int $userId): array
    {
        [$codes, $hashes] = self::recoveryCodes();
        $this->db->run('UPDATE user_two_factor SET recovery_codes = ? WHERE user_id = ?', [json_encode($hashes), $userId]);
        return $codes;
    }

    /**
     * Check a sign-in code: an authenticator code, or a recovery code
     * (which is used up). Returns 'totp', 'recovery' or null.
     */
    public function verify(int $userId, string $code): ?string
    {
        return $this->db->transaction(function () use ($userId, $code): ?string {
            $row = $this->db->fetch('SELECT secret, recovery_codes, last_step FROM user_two_factor WHERE user_id = ?', [$userId]);
            if ($row === null) {
                return null;
            }
            $step = $this->matchStep($this->crypto->decrypt((string) $row['secret']), $code, (int) $row['last_step']);
            if ($step !== null) {
                $this->db->run('UPDATE user_two_factor SET last_step = ? WHERE user_id = ?', [$step, $userId]);
                return 'totp';
            }
            $normalized = strtolower(preg_replace('/[^A-Za-z0-9]/', '', $code) ?? '');
            if (strlen($normalized) !== 10) {
                return null;
            }
            $hash = hash('sha256', $normalized);
            $hashes = array_values(array_filter((array) json_decode((string) $row['recovery_codes'], true), 'is_string'));
            foreach ($hashes as $i => $h) {
                if (hash_equals($h, $hash)) {
                    unset($hashes[$i]);
                    $this->db->run('UPDATE user_two_factor SET recovery_codes = ? WHERE user_id = ?', [json_encode(array_values($hashes)), $userId]);
                    return 'recovery';
                }
            }
            return null;
        });
    }

    /** @return array{0: list<string>, 1: list<string>} codes and their hashes */
    private static function recoveryCodes(): array
    {
        $codes = $hashes = [];
        for ($i = 0; $i < self::RECOVERY_CODES; $i++) {
            $raw = strtolower(substr(self::base32Encode(random_bytes(8)), 0, 10));
            $codes[] = substr($raw, 0, 5) . '-' . substr($raw, 5);
            $hashes[] = hash('sha256', $raw);
        }
        return [$codes, $hashes];
    }

    public static function base32Encode(string $bin): string
    {
        $bits = '';
        foreach (str_split($bin) as $c) {
            $bits .= str_pad(decbin(ord($c)), 8, '0', STR_PAD_LEFT);
        }
        $out = '';
        foreach (str_split($bits, 5) as $chunk) {
            $out .= self::B32[bindec(str_pad($chunk, 5, '0'))];
        }
        return $out;
    }

    public static function base32Decode(string $b32): string
    {
        $b32 = strtoupper(rtrim(str_replace(' ', '', $b32), '='));
        $bits = '';
        foreach (str_split($b32) as $c) {
            $v = strpos(self::B32, $c);
            if ($v === false) {
                throw new \InvalidArgumentException('Invalid base32 secret.');
            }
            $bits .= str_pad(decbin($v), 5, '0', STR_PAD_LEFT);
        }
        $out = '';
        foreach (str_split($bits, 8) as $byte) {
            if (strlen($byte) === 8) {
                $out .= chr(bindec($byte));
            }
        }
        return $out;
    }
}
