<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Database\Connection;

/**
 * Hosting options of an account. Websites without an owner (the
 * administrators') use the defaults.
 */
final class AccountOptions
{
    public const DEFAULTS = [
        // DNS for new websites
        'dns_dkim' => true,
        'dns_spf' => true,
        'dns_dmarc' => true,
        // The domain uses its registrar's name servers: no zone is created here.
        'dns_registrar_ns' => false,
        // Replace an existing zone's records when a website is created for it.
        'dns_overwrite' => false,
        // Mail: auto, local, backup or remote exchanger
        'mail_routing' => 'auto',
        'spam_filter' => true,
        'spam_box' => false,
    ];

    public function __construct(private readonly Connection $db)
    {
    }

    /** @return array<string, mixed> */
    public function get(?int $userId): array
    {
        if ($userId === null) {
            return self::DEFAULTS;
        }
        $raw = $this->db->scalar('SELECT options FROM account_options WHERE user_id = ?', [$userId]);
        $stored = is_string($raw) ? (array) json_decode($raw, true) : [];
        return array_intersect_key($stored, self::DEFAULTS) + self::DEFAULTS;
    }

    /** @param array<string, mixed> $options only known keys are kept */
    public function set(int $userId, array $options): void
    {
        $clean = [];
        foreach (self::DEFAULTS as $k => $def) {
            if (!array_key_exists($k, $options)) {
                continue;
            }
            $v = $options[$k];
            if (is_bool($def)) {
                $clean[$k] = filter_var($v, FILTER_VALIDATE_BOOL);
            } elseif ($k === 'mail_routing') {
                $clean[$k] = in_array($v, ['auto', 'local', 'backup', 'remote'], true) ? $v : 'auto';
            }
        }
        $merged = $clean + $this->get($userId);
        $this->db->run(
            'INSERT INTO account_options (user_id, options, updated_at) VALUES (?, ?, ?) ON CONFLICT(user_id) DO UPDATE SET options = excluded.options, updated_at = excluded.updated_at',
            [$userId, json_encode(array_intersect_key($merged, self::DEFAULTS)), gmdate('c')]
        );
    }
}
