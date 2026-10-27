<?php

declare(strict_types=1);

namespace NimVor\Security;

use NimVor\Services\SettingsService;

/**
 * The server's password policy (Security → Password Strength
 * Configuration): a minimum strength score and minimum counts of kinds of
 * characters, on top of PasswordHasher's base rules. It applies to panel
 * accounts, mailboxes (webmail) and the server's root password.
 */
final class PasswordPolicy
{
    /** Setting key => [label, pattern counting the characters]. */
    public const COUNTS = [
        'password_min_special' => ['special', '/[^A-Za-z0-9]/u'],
        'password_min_numeric' => ['numeric', '/[0-9]/'],
        'password_min_alpha' => ['alphabet', '/[A-Za-z]/'],
        'password_min_upper' => ['uppercase', '/[A-Z]/'],
        'password_min_lower' => ['lowercase', '/[a-z]/'],
    ];

    /** A strength at or above this is reported as strong (Security overview). */
    public const STRONG = 65;

    public function __construct(
        private readonly PasswordHasher $hasher,
        private readonly SettingsService $settings,
    ) {
    }

    /**
     * Strength 0–100: roughly the password's entropy in bits, from its
     * length (repetition counts less) and the kinds of characters used.
     */
    public static function strength(string $password): int
    {
        $chars = mb_str_split($password);
        if ($chars === []) {
            return 0;
        }
        $pool = (preg_match('/[a-z]/', $password) ? 26 : 0) + (preg_match('/[A-Z]/', $password) ? 26 : 0)
            + (preg_match('/[0-9]/', $password) ? 10 : 0) + (preg_match('/[^A-Za-z0-9]/u', $password) ? 33 : 0);
        $length = min(count($chars), 2 * count(array_unique($chars)));
        return (int) min(100, floor($length * log(max(2, $pool), 2)));
    }

    public function enabled(): bool
    {
        return $this->settings->bool('policy_password_strength');
    }

    /** @return array{strength: int, length: int, counts: array<string, int>} */
    public function rules(): array
    {
        $counts = [];
        foreach (array_keys(self::COUNTS) as $k) {
            $counts[$k] = $this->settings->int($k);
        }
        return ['strength' => $this->settings->int('password_min_strength'), 'length' => max(PasswordHasher::MIN_LENGTH, $this->settings->int('password_min_length')), 'counts' => $counts];
    }

    /** A user-facing problem with the password, or null when it is acceptable. */
    public function check(string $password, string $username = ''): ?string
    {
        if (($weak = $this->hasher->weakness($password, $username)) !== null || !$this->enabled()) {
            return $weak;
        }
        $r = $this->rules();
        if (mb_strlen($password) < $r['length']) {
            return 'The password must be at least ' . $r['length'] . ' characters long.';
        }
        foreach (self::COUNTS as $key => [$label, $re]) {
            if ($r['counts'][$key] > 0 && preg_match_all($re, $password) < $r['counts'][$key]) {
                return sprintf('The password needs at least %d %s character%s.', $r['counts'][$key], $label, $r['counts'][$key] === 1 ? '' : 's');
            }
        }
        if (($s = self::strength($password)) < $r['strength']) {
            return sprintf('The password is too weak (strength %d, at least %d is required). Make it longer or mix upper and lower case, digits and symbols.', $s, $r['strength']);
        }
        return null;
    }

    /** One line describing the rules, for password forms. */
    public function describe(): string
    {
        if (!$this->enabled()) {
            return 'At least ' . PasswordHasher::MIN_LENGTH . ' characters.';
        }
        $r = $this->rules();
        $parts = ['at least ' . $r['length'] . ' characters'];
        foreach (self::COUNTS as $key => [$label]) {
            if ($r['counts'][$key] > 0) {
                $parts[] = $r['counts'][$key] . ' ' . $label;
            }
        }
        return ucfirst(implode(', ', $parts)) . ($r['strength'] > 0 ? '; strength ' . $r['strength'] . ' or more.' : '.');
    }

    /**
     * A random password that meets the policy.
     *
     * @param string $exclude characters it must not contain
     */
    public function generate(int $length = 24, string $exclude = ''): string
    {
        $sets = ['abcdefghijkmnopqrstuvwxyz', 'ABCDEFGHJKLMNPQRSTUVWXYZ', '23456789', '!#%+-.=?@^_~'];
        $sets = array_map(static fn (string $s): string => str_replace(str_split($exclude !== '' ? $exclude : "\0"), '', $s), $sets);
        $r = $this->rules();
        $length = max($length, $r['length'], array_sum($r['counts']) + 4);
        for ($try = 0; $try < 100; $try++) {
            $chars = [];
            foreach ($sets as $set) {
                // Enough of each kind, then random from all.
                $need = max(2, $r['counts']['password_min_special'] ?? 0, $r['counts']['password_min_numeric'] ?? 0, $r['counts']['password_min_upper'] ?? 0, $r['counts']['password_min_lower'] ?? 0);
                for ($i = 0; $i < $need; $i++) {
                    $chars[] = $set[random_int(0, strlen($set) - 1)];
                }
            }
            $all = implode('', $sets);
            while (count($chars) < $length) {
                $chars[] = $all[random_int(0, strlen($all) - 1)];
            }
            for ($i = count($chars) - 1; $i > 0; $i--) {
                $j = random_int(0, $i);
                [$chars[$i], $chars[$j]] = [$chars[$j], $chars[$i]];
            }
            $password = implode('', $chars);
            if ($this->check($password) === null) {
                return $password;
            }
        }
        throw new \RuntimeException('Could not generate a password that meets the policy.');
    }
}
