<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * Site access: password-protect a website, or one path of it, with HTTP basic
 * authentication. Visitors get the browser's sign-in prompt and need the user
 * name and password set here. The agent writes the Nginx rule and keeps only
 * a SHA-512 crypt hash of the password in its own password file; the panel
 * never stores the password.
 */
final class SiteAccessService
{
    /** Most protected paths of one website (the agent's limit). */
    public const MAX_PATHS = 20;

    public function __construct(
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * The website's protected paths (user names only).
     *
     * @param array<string, mixed> $site
     * @return list<array{path: string, user: string, realm: string}>
     */
    public function paths(array $site): array
    {
        $res = (array) $this->agent->call('protect.status', ['domain' => (string) $site['domain']]);
        $out = [];
        foreach ((array) ($res['password'] ?? []) as $p) {
            $p = (array) $p;
            $out[] = ['path' => (string) ($p['path'] ?? '/'), 'user' => (string) ($p['user'] ?? ''), 'realm' => (string) ($p['realm'] ?? '')];
        }
        return $out;
    }

    /** "/" for the whole website, else "/some/folder/": the URL path the agent stores. */
    public static function cleanPath(string $path): string
    {
        $path = '/' . trim(trim($path), '/') . '/';
        if ($path === '//') {
            $path = '/';
        }
        if (preg_match('#^/([A-Za-z0-9._-]+/)*$#', $path) !== 1 || str_contains($path, '/../') || str_contains($path, '/./') || strlen($path) > 200) {
            throw new ValidationException(['path' => 'Use a URL path such as / (the whole website) or /admin/ — letters, digits, dots, dashes and underscores only.']);
        }
        return $path;
    }

    /**
     * Turns protection of a path on, or changes its user and password.
     *
     * @param array<string, mixed> $site
     */
    public function protect(array $site, string $path, string $user, string $password, string $realm, int $userId): string
    {
        self::writable($site);
        $errors = [];
        try {
            $path = self::cleanPath($path);
        } catch (ValidationException $e) {
            $errors += $e->errors();
        }
        if (preg_match('/^[A-Za-z0-9._-]{1,64}$/', $user) !== 1) {
            $errors['user'] = 'Use up to 64 letters, digits, dots, dashes or underscores.';
        }
        if (strlen($password) < 8 || strlen($password) > 128 || preg_match('/[\x00\r\n]/', $password) === 1) {
            $errors['password'] = 'Use 8 to 128 characters.';
        }
        $realm = trim($realm) === '' ? 'Restricted area' : trim($realm);
        if (preg_match('/^[A-Za-z0-9 .,()-]{1,64}$/', $realm) !== 1) {
            $errors['realm'] = 'Use letters, digits, spaces and . , ( ) - only.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $this->agent->call('protect.password', ['domain' => (string) $site['domain'], 'path' => $path, 'enabled' => true, 'user' => $user, 'password' => $password, 'realm' => $realm], 120);
        $this->audit->record('site_access.protect', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'path' => $path, 'user' => $user], $userId);
        return 'Site access is on: visitors to ' . $site['domain'] . rtrim($path, '/') . '/ must sign in as ' . $user . '.';
    }

    /** @param array<string, mixed> $site */
    public function remove(array $site, string $path, int $userId): string
    {
        self::writable($site);
        $path = self::cleanPath($path);
        $this->agent->call('protect.password', ['domain' => (string) $site['domain'], 'path' => $path, 'enabled' => false], 120);
        $this->audit->record('site_access.remove', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'path' => $path], $userId);
        return 'Site access protection of ' . $site['domain'] . rtrim($path, '/') . '/ is off.';
    }

    /** @param array<string, mixed> $site */
    private static function writable(array $site): void
    {
        if (!empty($site['account_suspended'])) {
            throw new UserFacingException('The hosting account is suspended; site access cannot be changed.', status: 409);
        }
    }
}
