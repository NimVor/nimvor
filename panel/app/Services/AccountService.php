<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Database\Connection;
use NimVor\Hosting\Scope;
use NimVor\Repositories\UserRepository;
use NimVor\Support\ValidationException;

/**
 * Creates a hosting account in one step: the panel login (a hosting customer, or a reseller), its plan
 * and hosting options, and its first website for the domain, run by a new
 * system account of the same name. The website then gets its DNS zone,
 * email and automatic SSL (SiteProvisioner, AutoSslService).
 *
 * If the website cannot be created, the login is removed again.
 */
final class AccountService
{
    private const RESERVED = ['root', 'www-data', 'nobody', 'mysql', 'nimvor', 'admin', 'postfix', 'dovecot', 'bind', 'named', 'redis', 'memcache'];

    public function __construct(
        private readonly UserService $users,
        private readonly UserRepository $userRepo,
        private readonly WebsiteService $sites,
        private readonly AccountOptions $options,
        private readonly Connection $db,
        private readonly AuditLogger $audit,
        private readonly ?SettingsService $settings = null,
    ) {
    }

    /**
     * @param array<string, mixed> $input
     * @param array<string, mixed> $actor
     * @return array{id: int, website_id: int|null, password: string|null}
     */
    public function create(array $input, array $actor): array
    {
        $username = strtolower(trim((string) ($input['username'] ?? '')));
        $domain = strtolower(trim(rtrim((string) ($input['domain'] ?? ''), '.')));
        $errors = [];
        // Also the name of the website's system account.
        if (!preg_match('/^[a-z][a-z0-9_-]{2,30}$/', $username) || in_array($username, ['root', 'www-data', 'nobody', 'mysql', 'nimvor', 'admin', 'postfix', 'dovecot', 'bind', 'named', 'redis', 'memcache'], true)) {
            $errors['username'] = 'Use 3–31 lowercase letters, digits, - or _, starting with a letter (also the website\'s system account).';
        }
        $reseller = filter_var($input['reseller'] ?? false, FILTER_VALIDATE_BOOL);
        if ($reseller && !Scope::isAll($actor)) {
            $errors['reseller'] = 'Only administrators create resellers.';
        }
        $generated = null;
        if (filter_var($input['generate'] ?? false, FILTER_VALIDATE_BOOL)) {
            $generated = self::password();
            $input['password'] = $input['password_confirmation'] = $generated;
        }
        if ($domain !== '') {
            // Checked before anything is created (format, not taken).
            try {
                $this->sites->validate(['domain' => $domain, 'kind' => 'static', 'system_user' => $username], null);
            } catch (ValidationException $e) {
                $errors += array_intersect_key($e->errors(), ['domain' => 1, 'aliases' => 1]);
            }
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $role = (int) $this->db->scalar('SELECT id FROM roles WHERE name = ?', [$reseller ? 'reseller' : 'customer']);
        $id = $this->users->create([
            'username' => $username,
            'email' => $input['email'] ?? '',
            'display_name' => $input['display_name'] ?? '',
            'password' => $input['password'] ?? '',
            'password_confirmation' => $input['password_confirmation'] ?? '',
            'role_id' => (string) $role,
            'is_active' => true,
            'plan_id' => $input['plan_id'] ?? '',
        ], (int) $actor['id']);
        $this->options->set($id, $this->optionsFrom($input));

        $websiteId = null;
        if ($domain !== '') {
            try {
                // As the new account: its plan and domain rules apply.
                $account = (array) $this->userRepo->find($id);
                $websiteId = $this->sites->create([
                    'domain' => $domain,
                    'kind' => 'php',
                    'php_version' => (string) ($input['php_version'] ?? '') ?: ($this->sites->phpVersions()[0] ?? ''),
                    'system_user' => $username,
                    'new_system_user' => true,
                    'aliases' => filter_var($input['www'] ?? true, FILTER_VALIDATE_BOOL) ? 'www.' . $domain : '',
                ], $account);
            } catch (\Throwable $e) {
                $this->userRepo->delete($id);
                $this->audit->record('account.create', 'user', $username, AuditLogger::FAILURE, ['domain' => $domain, 'error' => $e->getMessage()]);
                throw $e;
            }
        }
        $this->audit->record('account.create', 'user', (string) $id, AuditLogger::SUCCESS, ['username' => $username, 'domain' => $domain, 'reseller' => $reseller]);
        return ['id' => $id, 'website_id' => $websiteId, 'password' => $generated];
    }

    /**
     * A website created in the wizard for a customer who does not exist yet: the customer's panel login is created first (named
     * after the domain unless given, with a generated password the caller shows once, on the account's default plan), then the
     * website runs as that account, so its plan and domain rules apply. If the website cannot be created the login is removed again.
     *
     * @param array<string, mixed> $site the website's fields, as for WebsiteService::create()
     * @param array<string, mixed> $actor the administrator
     * @return array{id: int, site_id: int, username: string, password: string}
     * @throws ValidationException new_username and new_email are the fields to correct; others are the website's
     */
    public function createCustomerWebsite(array $site, string $username, string $email, array $actor): array
    {
        $domain = strtolower(trim(rtrim((string) ($site['domain'] ?? ''), '.')));
        $username = strtolower(trim($username));
        $errors = [];
        if ($username === '') {
            $username = $this->usernameFor($domain);
        } elseif (!preg_match('/^[a-z][a-z0-9_-]{2,30}$/', $username) || in_array($username, self::RESERVED, true)) {
            $errors['new_username'] = 'Use 3–31 lowercase letters, digits, - or _, starting with a letter (also the website\'s system account).';
        }
        if (!filter_var(trim($email), FILTER_VALIDATE_EMAIL)) {
            $errors['new_email'] = 'Enter the customer\'s email address.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $password = self::password();
        $planId = $this->settings !== null ? $this->settings->int('default_plan_id') : 0;
        $role = (int) $this->db->scalar('SELECT id FROM roles WHERE name = ?', ['customer']);
        try {
            $id = $this->users->create([
                'username' => $username, 'email' => trim($email), 'display_name' => '', 'password' => $password,
                'password_confirmation' => $password, 'role_id' => (string) $role, 'is_active' => true, 'plan_id' => $planId > 0 ? (string) $planId : '',
            ], (int) $actor['id']);
        } catch (ValidationException $e) {
            $map = ['username' => 'new_username', 'email' => 'new_email'];
            throw new ValidationException(array_combine(array_map(static fn (string $k): string => $map[$k] ?? $k, array_keys($e->errors())), $e->errors()));
        }
        try {
            $account = (array) $this->userRepo->find($id);
            $siteId = $this->sites->create($site + ['system_user' => $username, 'new_system_user' => true], $account);
        } catch (ValidationException $e) {
            $this->userRepo->delete($id);
            $errs = $e->errors();
            if (isset($errs['system_user'])) {
                $errs['new_username'] = $errs['system_user'];
                unset($errs['system_user']);
            }
            throw new ValidationException($errs);
        } catch (\Throwable $e) {
            $this->userRepo->delete($id);
            $this->audit->record('account.create', 'user', $username, AuditLogger::FAILURE, ['domain' => $domain, 'error' => $e->getMessage()]);
            throw $e;
        }
        $this->audit->record('account.create', 'user', (string) $id, AuditLogger::SUCCESS, ['username' => $username, 'domain' => $domain, 'via' => 'website wizard']);
        return ['id' => $id, 'site_id' => $siteId, 'username' => $username, 'password' => $password];
    }

    /** A free login name for a domain: its first label (letters and digits), made unique with a number. */
    private function usernameFor(string $domain): string
    {
        $base = preg_replace('/[^a-z0-9_-]/', '', explode('.', $domain)[0] ?? '') ?: 'site';
        if (!preg_match('/^[a-z]/', $base)) {
            $base = 'u' . $base;
        }
        $base = substr(str_pad($base, 3, 'x'), 0, 28);
        $name = in_array($base, self::RESERVED, true) ? $base . '1' : $base;
        for ($n = 2; $n < 200 && $this->userRepo->usernameExists($name); $n++) {
            $name = $base . $n;
        }
        return $name;
    }

    /**
     * @param array<string, mixed> $input
     * @return array<string, mixed>
     */
    public function optionsFrom(array $input): array
    {
        $out = [];
        foreach (AccountOptions::DEFAULTS as $k => $def) {
            if (is_bool($def)) {
                $out[$k] = filter_var($input[$k] ?? false, FILTER_VALIDATE_BOOL);
            }
        }
        $out['mail_routing'] = (string) ($input['mail_routing'] ?? 'auto');
        // DMARC needs DKIM and SPF.
        $out['dns_dmarc'] = $out['dns_dmarc'] && $out['dns_dkim'] && $out['dns_spf'];
        return $out;
    }

    /** A strong password: 20 characters from an unambiguous set. */
    public static function password(): string
    {
        $chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789-_.!@#%';
        $out = '';
        for ($i = 0; $i < 20; $i++) {
            $out .= $chars[random_int(0, strlen($chars) - 1)];
        }
        return $out;
    }
}
