<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * FTP accounts (virtual pure-ftpd logins named <name>@<domain>) and SFTP access of one website. The web
 * interface and the REST API both call this, so both validate, limit and audit identically.
 */
final class FtpAccountService
{
    /** Accounts per website (a safety limit, not a plan limit). */
    public const MAX_ACCOUNTS = 25;

    public function __construct(
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly PlanLimits $limits,
        private readonly FtpUsage $usage,
    ) {
    }

    /** The login name of an account of this website: <name>@<domain>. */
    public static function login(string $name, string $domain): string
    {
        return strtolower(trim($name)) . '@' . strtolower($domain);
    }

    /**
     * What the agent knows about this website's FTP and SFTP access.
     *
     * @param array<string, mixed> $site
     * @return array<string, mixed>
     */
    public function status(array $site): array
    {
        return (array) $this->agent->call('ftp.status', ['user' => (string) $site['system_user']]);
    }

    /**
     * Creates or changes an account.
     *
     * @param array<string, mixed> $site
     * @param array{name: string, dir?: string, password?: string, generate?: bool, quota_mb?: int|string} $in
     * @return array{login: string, created: bool, password: ?string, generated: bool}
     */
    public function save(array $site, array $in, bool $existing): array
    {
        $domain = (string) $site['domain'];
        $name = strtolower(trim((string) ($in['name'] ?? '')));
        if ($existing) {
            // Editing: the full login name comes back as is.
            $login = $name;
            if (!str_ends_with($login, '@' . strtolower($domain))) {
                throw new UserFacingException('This account does not belong to the website.');
            }
        } else {
            if (!preg_match('/^[a-z0-9][a-z0-9._-]{0,30}$/', $name)) {
                throw new ValidationException(['name' => 'Use up to 31 lower-case letters, digits and . _ -']);
            }
            $login = self::login($name, $domain);
            if (strlen($login) > 64) {
                throw new ValidationException(['name' => 'The full login (' . $login . ') would be longer than 64 characters.']);
            }
        }
        $dir = trim((string) ($in['dir'] ?? ''), " /");
        if (preg_match('#(^|/)\.\.(/|$)#', $dir) || strlen($dir) > 300) {
            throw new ValidationException(['dir' => 'Enter a folder inside the website, e.g. public_html.']);
        }
        $password = (string) ($in['password'] ?? '');
        $generated = false;
        if (!empty($in['generate'])) {
            $password = rtrim(strtr(base64_encode(random_bytes(15)), '+/', 'Aa'), '=');
            $generated = true;
        }
        if ($password !== '' && (strlen($password) < 10 || strlen($password) > 128)) {
            throw new ValidationException(['password' => 'Use 10–128 characters, or generate one.']);
        }
        if (!$existing) {
            if ($password === '') {
                throw new ValidationException(['password' => 'Enter a password or generate one.']);
            }
            if (count((array) ($this->status($site)['accounts'] ?? [])) >= self::MAX_ACCOUNTS) {
                throw new UserFacingException('This website already has ' . self::MAX_ACCOUNTS . ' FTP accounts.');
            }
            $this->limits->assertCanCreate('ftp_accounts', $site['owner_id'] === null ? null : (int) $site['owner_id'], 1, fn (array $owners): int => $this->usage->count($owners));
        }
        $quota = max(0, (int) ($in['quota_mb'] ?? 0));
        $params = ['name' => $login, 'user' => (string) $site['system_user'], 'dir' => $dir, 'quota_mb' => $quota];
        if ($password !== '') {
            $params['password'] = $password;
        }
        $this->agent->call('ftp.account_save', $params);
        $this->audit->record($existing ? 'ftp.account_update' : 'ftp.account_create', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['account' => $login, 'dir' => $dir, 'quota_mb' => $quota, 'password_changed' => $password !== '']);
        return ['login' => $login, 'created' => !$existing, 'password' => $generated ? $password : null, 'generated' => $generated];
    }

    /** @param array<string, mixed> $site */
    public function delete(array $site, string $login): string
    {
        $login = strtolower(trim($login));
        if (!str_ends_with($login, '@' . strtolower((string) $site['domain']))) {
            throw new UserFacingException('This account does not belong to the website.');
        }
        $this->agent->call('ftp.account_delete', ['name' => $login, 'user' => (string) $site['system_user']]);
        $this->audit->record('ftp.account_delete', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['account' => $login]);
        return $login;
    }

    /**
     * @param array<string, mixed> $site
     * @param list<string> $keys
     */
    public function sftp(array $site, bool $enabled, array $keys, bool $passwordLogin, string $password): void
    {
        $keys = array_values(array_filter(array_map('trim', $keys), static fn (string $k): bool => $k !== '' && !str_starts_with($k, '#')));
        if ($password !== '' && (strlen($password) < 10 || strlen($password) > 128)) {
            throw new ValidationException(['sftp_password' => 'Use 10–128 characters.']);
        }
        $params = ['user' => (string) $site['system_user'], 'enabled' => $enabled, 'keys' => $keys, 'password_login' => $passwordLogin];
        if ($password !== '') {
            $params['password'] = $password;
        }
        $this->agent->call('sftp.configure', $params, 60);
        $this->audit->record('sftp.configure', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['enabled' => $enabled, 'keys' => count($keys), 'password_login' => $passwordLogin, 'password_changed' => $password !== '']);
    }
}
