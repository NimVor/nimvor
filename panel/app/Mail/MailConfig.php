<?php

declare(strict_types=1);

namespace WhaCP\Mail;

use WhaCP\Repositories\SettingRepository;
use WhaCP\Security\Crypto;
use WhaCP\Services\AuditLogger;
use WhaCP\Support\ValidationException;

/**
 * Outgoing mail (SMTP) settings. The password is stored encrypted with the
 * panel key and never shown again.
 */
final class MailConfig
{
    public const ENCRYPTION = ['tls' => 'TLS (port 465)', 'starttls' => 'STARTTLS (port 587)', 'none' => 'None (local relay only)'];
    private const PREFIX = 'mail.';

    public function __construct(
        private readonly SettingRepository $settings,
        private readonly Crypto $crypto,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @return array{host: string, port: int, encryption: string, username: string, has_password: bool, from: string, alert_to: string} */
    public function get(): array
    {
        $all = $this->settings->all();
        $v = static fn (string $k, mixed $d): mixed => $all[self::PREFIX . $k] ?? $d;
        return [
            'host' => (string) $v('host', ''),
            'port' => (int) $v('port', 587),
            'encryption' => (string) $v('encryption', 'starttls'),
            'username' => (string) $v('username', ''),
            'has_password' => (string) $v('password', '') !== '',
            'from' => (string) $v('from', ''),
            'alert_to' => (string) $v('alert_to', ''),
        ];
    }

    public function password(): string
    {
        $enc = (string) ($this->settings->all()[self::PREFIX . 'password'] ?? '');
        return $enc === '' ? '' : $this->crypto->decrypt($enc);
    }

    /** @return list<string> */
    public function alertRecipients(): array
    {
        return self::addresses($this->get()['alert_to']);
    }

    /** @return list<string> */
    public static function addresses(string $list): array
    {
        return array_values(array_filter(array_map('trim', preg_split('/[,;\s]+/', $list) ?: []), static fn (string $a): bool => $a !== ''));
    }

    public static function validAddress(string $a): bool
    {
        return strlen($a) <= 254 && filter_var($a, FILTER_VALIDATE_EMAIL) !== false && !preg_match('/[\r\n<>]/', $a);
    }

    /**
     * @param array<string, mixed> $input host, port, encryption, username, password (blank keeps), from, alert_to, clear_password
     */
    public function save(array $input, ?int $userId): void
    {
        $s = static fn (string $k): string => is_scalar($input[$k] ?? null) ? trim((string) $input[$k]) : '';
        $host = strtolower($s('host'));
        $port = filter_var($s('port'), FILTER_VALIDATE_INT, ['options' => ['min_range' => 1, 'max_range' => 65535]]);
        $enc = $s('encryption');
        $errors = [];
        if ($host !== '' && !preg_match('/^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/', $host) && filter_var($host, FILTER_VALIDATE_IP) === false) {
            $errors['host'] = 'Enter a host name or IP address.';
        }
        if ($port === false) {
            $errors['port'] = 'Enter a port between 1 and 65535.';
        }
        if (!isset(self::ENCRYPTION[$enc])) {
            $errors['encryption'] = 'Choose an encryption mode.';
        } elseif ($enc === 'none' && $host !== '' && !in_array($host, ['localhost', '127.0.0.1', '::1'], true)) {
            $errors['encryption'] = 'Unencrypted SMTP is only allowed to a relay on this server.';
        }
        if (mb_strlen($s('username')) > 200 || preg_match('/[\x00-\x1F\x7F]/', $s('username') . (string) ($input['password'] ?? ''))) {
            $errors['username'] = 'The user name or password contains invalid characters.';
        }
        if ($host !== '' && !self::validAddress($s('from'))) {
            $errors['from'] = 'Enter the sender address, e.g. panel@example.com.';
        }
        foreach (self::addresses($s('alert_to')) as $a) {
            if (!self::validAddress($a)) {
                $errors['alert_to'] = "\"{$a}\" is not a valid email address.";
            }
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $values = ['host' => $host, 'port' => (int) $port, 'encryption' => $enc, 'username' => $s('username'), 'from' => $s('from'), 'alert_to' => implode(', ', self::addresses($s('alert_to')))];
        foreach ($values as $k => $v) {
            $this->settings->set(self::PREFIX . $k, $v, $userId);
        }
        $password = is_string($input['password'] ?? null) ? $input['password'] : '';
        if (($input['clear_password'] ?? '') === '1') {
            $this->settings->set(self::PREFIX . 'password', '', $userId);
        } elseif ($password !== '') {
            $this->settings->set(self::PREFIX . 'password', $this->crypto->encrypt($password), $userId);
        }
        $this->audit->record('settings.mail', 'settings', 'mail', AuditLogger::SUCCESS, $values + ['password_changed' => $password !== '' || ($input['clear_password'] ?? '') === '1']);
    }
}
