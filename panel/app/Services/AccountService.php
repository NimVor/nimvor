<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Database\Connection;
use WhaCP\Hosting\Scope;
use WhaCP\Repositories\UserRepository;
use WhaCP\Support\ValidationException;

/**
 * Creates a hosting account in one step (like WHM's "Create a New
 * Account"): the panel login (a hosting customer, or a reseller), its plan
 * and hosting options, and its first website for the domain, run by a new
 * system account of the same name. The website then gets its DNS zone,
 * email and automatic SSL (SiteProvisioner, AutoSslService).
 *
 * If the website cannot be created, the login is removed again.
 */
final class AccountService
{
    public function __construct(
        private readonly UserService $users,
        private readonly UserRepository $userRepo,
        private readonly WebsiteService $sites,
        private readonly AccountOptions $options,
        private readonly Connection $db,
        private readonly AuditLogger $audit,
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
        if (!preg_match('/^[a-z][a-z0-9_-]{2,30}$/', $username) || in_array($username, ['root', 'www-data', 'nobody', 'mysql', 'whacp', 'admin', 'postfix', 'dovecot', 'bind', 'named', 'redis', 'memcache'], true)) {
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
