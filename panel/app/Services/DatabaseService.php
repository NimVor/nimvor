<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Repositories\DatabaseRepository;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Security\Crypto;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * MySQL/MariaDB databases and users.
 *
 * Passwords are generated here, sent to the agent, and shown to the user
 * once. They are only kept (encrypted) when the user explicitly asks.
 * Hosting customers' names are prefixed with their username so they can
 * never collide with, or guess at, other customers' databases.
 */
final class DatabaseService
{
    public const PRIVILEGES = ['all' => 'All privileges', 'readwrite' => 'Read & write (no GRANT)', 'readonly' => 'Read only'];

    public function __construct(
        private readonly DatabaseRepository $repo,
        private readonly WebsiteRepository $sites,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
        private readonly Crypto $crypto,
        private readonly PlanLimits $limits,
        private readonly ?SettingsService $settings = null,
    ) {
    }

    /**
     * Whether the actor may let database users connect from other hosts (Settings).
     *
     * @param array<string, mixed> $actor
     */
    public function remoteHostsAllowed(array $actor): bool
    {
        return Scope::isAll($actor) || $this->settings === null || $this->settings->bool('db_customer_remote_hosts');
    }

    /** @param array<string, mixed> $actor */
    private function allowedHost(string $host, array $actor): string
    {
        if ($host !== 'localhost' && !$this->remoteHostsAllowed($actor)) {
            throw new ValidationException(['host' => 'Database users may connect from this server only (your hosting provider\'s setting).']);
        }
        return $host;
    }

    /** @param array<string, mixed> $actor */
    public function prefix(array $actor): string
    {
        return Scope::isAll($actor) ? '' : substr(preg_replace('/[^a-z0-9_]/', '', strtolower((string) $actor['username'])) ?? '', 0, 16) . '_';
    }

    /**
     * Create a database, optionally with a user granted full access.
     *
     * @param array<string, mixed> $input
     * @param array<string, mixed> $actor
     * @return array{database_id: int, user?: string, password?: string}
     */
    public function create(array $input, array $actor): array
    {
        $serverId = $this->server->id();
        $prefix = $this->prefix($actor);
        $name = $prefix . strtolower(trim((string) ($input['name'] ?? '')));
        $withUser = filter_var($input['create_user'] ?? false, FILTER_VALIDATE_BOOL);
        $username = $withUser ? $prefix . strtolower(trim((string) ($input['username'] ?? '') ?: substr((string) ($input['name'] ?? ''), 0, 16))) : '';
        $store = filter_var($input['store_password'] ?? false, FILTER_VALIDATE_BOOL);
        $websiteId = filter_var($input['website_id'] ?? null, FILTER_VALIDATE_INT) ?: null;

        $errors = [];
        if (!preg_match('/^[a-z0-9_]{1,64}$/', $name) || in_array($name, ['mysql', 'sys', 'information_schema', 'performance_schema'], true)) {
            $errors['name'] = 'Use up to 64 lowercase letters, digits or underscores.';
        } elseif ($this->repo->nameTaken($serverId, $name)) {
            $errors['name'] = 'A database with this name already exists.';
        }
        if ($withUser && (!preg_match('/^[a-z0-9_]{1,32}$/', $username) || $this->repo->userTaken($serverId, $username, 'localhost'))) {
            $errors['username'] = 'Use up to 32 lowercase letters, digits or underscores (not already used).';
        }
        if ($websiteId !== null && $this->sites->find($serverId, $websiteId, $actor) === null) {
            $errors['website_id'] = 'Choose one of your websites.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $ownerId = Scope::isAll($actor) ? ($websiteId !== null ? ($this->sites->find($serverId, $websiteId, null)['owner_id'] ?? null) : null) : (int) $actor['id'];
        $this->limits->assertCanCreate('databases', $ownerId);

        $this->agent->call('db.create', ['name' => $name]);
        $dbId = $this->repo->insertDatabase($serverId, $name, $websiteId, $ownerId !== null ? (int) $ownerId : null);
        $this->audit->record('database.create', 'database', $name);
        $out = ['database_id' => $dbId];
        if ($withUser) {
            $password = Crypto::password();
            try {
                $this->agent->call('db.user_create', ['user' => $username, 'host' => 'localhost', 'password' => $password]);
                $this->agent->call('db.grant', ['user' => $username, 'host' => 'localhost', 'database' => $name, 'privileges' => 'all']);
            } catch (AgentException $e) {
                throw new UserFacingException('The database was created, but its user could not be: ' . $e->getMessage(), $e->reason(), 'Create the user from the Users tab.');
            }
            $uid = $this->repo->insertUser($serverId, $username, 'localhost', $ownerId !== null ? (int) $ownerId : null, $store ? $this->crypto->encrypt($password) : '');
            $this->repo->setGrant($dbId, $uid, 'all');
            $this->audit->record('database.user_create', 'database_user', $username, AuditLogger::SUCCESS, ['database' => $name, 'stored' => $store]);
            $out += ['user' => $username, 'password' => $password];
        }
        return $out;
    }

    /**
     * @param array<string, mixed> $input
     * @param array<string, mixed> $actor
     * @return array{user: string, password: string}
     */
    public function createUser(array $input, array $actor): array
    {
        $serverId = $this->server->id();
        $username = $this->prefix($actor) . strtolower(trim((string) ($input['username'] ?? '')));
        $host = $this->allowedHost(self::hostFrom($input, $actor), $actor);
        if (!preg_match('/^[a-z0-9_]{1,32}$/', $username) || $this->repo->userTaken($serverId, $username, $host)) {
            throw new ValidationException(['username' => 'Use up to 32 lowercase letters, digits or underscores (not already used).']);
        }
        $password = Crypto::password();
        $this->agent->call('db.user_create', ['user' => $username, 'host' => $host, 'password' => $password]);
        $store = filter_var($input['store_password'] ?? false, FILTER_VALIDATE_BOOL);
        $this->repo->insertUser($serverId, $username, $host, Scope::isAll($actor) ? null : (int) $actor['id'], $store ? $this->crypto->encrypt($password) : '');
        $this->audit->record('database.user_create', 'database_user', $username . '@' . $host, AuditLogger::SUCCESS, ['stored' => $store]);
        return ['user' => $username, 'password' => $password];
    }

    /**
     * Where a database user may connect from. The form offers "this server"
     * (localhost), "a specific address" and "any host" (%). Hosting
     * customers may give an IP address or an IPv4 network with % wildcards
     * (203.0.113.%); administrators may also use host names and netmasks.
     *
     * @param array<string, mixed> $input
     * @param array<string, mixed> $actor
     */
    public static function hostFrom(array $input, array $actor): string
    {
        $host = match ((string) ($input['host_choice'] ?? '')) {
            'localhost' => 'localhost',
            'any' => '%',
            'ip' => trim((string) ($input['host_ip'] ?? '')),
            default => trim((string) ($input['host'] ?? 'localhost')),
        };
        $ok = $host === 'localhost' || $host === '%'
            || filter_var($host, FILTER_VALIDATE_IP) !== false
            || preg_match('/^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d|%)){0,2}\.%$/', $host) === 1;
        if (!$ok && Scope::isAll($actor)) {
            $ok = preg_match('/^([0-9A-Fa-f.:%\/]{1,60}|[A-Za-z0-9.-]{1,60})$/', $host) === 1;
        }
        if (!$ok) {
            throw new ValidationException(['host' => Scope::isAll($actor)
                ? 'Use localhost, an IP address, a network or %.'
                : 'Enter an IP address (e.g. 203.0.113.5) or a network with % (e.g. 203.0.113.%).']);
        }
        return $host;
    }

    /**
     * Let an existing database user also connect from another host: a
     * separate account (same name, new host) with its own new password
     * and the same database access.
     *
     * @param array<string, mixed> $dbUser
     * @param array<string, mixed> $input
     * @param array<string, mixed> $actor
     * @return array{user: string, password: string}
     */
    public function addHost(array $dbUser, array $input, array $actor): array
    {
        $serverId = $this->server->id();
        $host = $this->allowedHost(self::hostFrom($input, $actor), $actor);
        if ($this->repo->userTaken($serverId, (string) $dbUser['username'], $host)) {
            throw new ValidationException(['host' => $dbUser['username'] . ' may already connect from ' . $host . '.']);
        }
        $password = Crypto::password();
        $this->agent->call('db.user_create', ['user' => $dbUser['username'], 'host' => $host, 'password' => $password]);
        $store = filter_var($input['store_password'] ?? false, FILTER_VALIDATE_BOOL);
        $newId = $this->repo->insertUser($serverId, (string) $dbUser['username'], $host, $dbUser['owner_id'] === null ? null : (int) $dbUser['owner_id'], $store ? $this->crypto->encrypt($password) : '');
        foreach ($this->repo->grantsOf((int) $dbUser['id']) as $g) {
            $this->agent->call('db.grant', ['user' => $dbUser['username'], 'host' => $host, 'database' => $g['name'], 'privileges' => $g['privileges'], 'revoke' => false]);
            $this->repo->setGrant((int) $g['database_id'], $newId, (string) $g['privileges']);
        }
        $this->audit->record('database.user_create', 'database_user', $dbUser['username'] . '@' . $host, AuditLogger::SUCCESS, ['stored' => $store, 'copied_from' => $dbUser['host']]);
        return ['user' => (string) $dbUser['username'], 'password' => $password];
    }

    /**
     * @param array<string, mixed> $dbUser
     * @return string the new password
     */
    public function resetPassword(array $dbUser, bool $store): string
    {
        $password = Crypto::password();
        $this->agent->call('db.user_password', ['user' => $dbUser['username'], 'host' => $dbUser['host'], 'password' => $password]);
        $this->repo->setPasswordEnc((int) $dbUser['id'], $store ? $this->crypto->encrypt($password) : '');
        $this->audit->record('database.user_password', 'database_user', $dbUser['username'] . '@' . $dbUser['host'], AuditLogger::SUCCESS, ['stored' => $store]);
        return $password;
    }

    /** @param array<string, mixed> $dbUser */
    public function revealPassword(array $dbUser): ?string
    {
        if ((string) $dbUser['password_enc'] === '') {
            return null;
        }
        $this->audit->record('database.password_reveal', 'database_user', $dbUser['username'] . '@' . $dbUser['host']);
        return $this->crypto->decrypt((string) $dbUser['password_enc']);
    }

    /**
     * @param array<string, mixed> $database
     * @param array<string, mixed> $dbUser
     */
    public function grant(array $database, array $dbUser, ?string $privileges): void
    {
        if ($privileges !== null && !isset(self::PRIVILEGES[$privileges])) {
            throw new ValidationException(['privileges' => 'Choose a privilege set.']);
        }
        $this->agent->call('db.grant', [
            'user' => $dbUser['username'], 'host' => $dbUser['host'], 'database' => $database['name'],
            'privileges' => $privileges ?? 'all', 'revoke' => $privileges === null,
        ]);
        $this->repo->setGrant((int) $database['id'], (int) $dbUser['id'], $privileges);
        $this->audit->record($privileges === null ? 'database.revoke' : 'database.grant', 'database', $database['name'], AuditLogger::SUCCESS, ['user' => $dbUser['username'], 'privileges' => $privileges]);
    }

    /** @param array<string, mixed> $database */
    public function drop(array $database, string $confirm): void
    {
        if ($confirm !== $database['name']) {
            throw new UserFacingException('Type the database name to confirm.');
        }
        $this->agent->call('db.drop', ['name' => $database['name'], 'confirm' => $database['name']]);
        $this->repo->deleteDatabase((int) $database['id']);
        $this->audit->record('database.drop', 'database', (string) $database['name']);
    }

    /** @param array<string, mixed> $dbUser */
    public function dropUser(array $dbUser): void
    {
        $this->agent->call('db.user_drop', ['user' => $dbUser['username'], 'host' => $dbUser['host']]);
        $this->repo->deleteUser((int) $dbUser['id']);
        $this->audit->record('database.user_drop', 'database_user', $dbUser['username'] . '@' . $dbUser['host']);
    }

    /**
     * Import a .sql or .sql.gz file of the database's website (a path in
     * its folder) as a job. Only an upload the panel itself named
     * (tmp/nimvor-import-….sql) is removed afterwards.
     *
     * @param array<string, mixed> $database
     * @param array<string, mixed> $site
     * @return string job id
     */
    public function importFromSite(array $database, array $site, string $path, bool $uploaded): string
    {
        $path = ltrim(trim($path), '/');
        if (!preg_match('/\.sql(\.gz)?$/', $path) || str_contains($path, '..') || strlen($path) > 255) {
            throw new ValidationException(['path' => 'Choose a .sql or .sql.gz file inside the website folder.']);
        }
        $remove = $uploaded && preg_match('#^tmp/nimvor-import-[a-z0-9]{16}\.sql(\.gz)?$#', $path) === 1;
        $res = (array) $this->agent->call('db.import_site', ['name' => $database['name'], 'domain' => $site['domain'], 'path' => $path, 'remove_after' => $remove]);
        $this->audit->record('database.import', 'database', (string) $database['name'], AuditLogger::SUCCESS, ['website' => $site['domain'], 'file' => $path]);
        return (string) $res['job_id'];
    }

    /**
     * @param array<string, mixed> $database
     * @return string job id
     */
    public function dump(array $database): string
    {
        $res = (array) $this->agent->call('db.dump', ['name' => $database['name']]);
        $this->audit->record('database.dump', 'database', (string) $database['name'], AuditLogger::SUCCESS, ['file' => $res['file'] ?? '']);
        return (string) $res['job_id'];
    }

    /**
     * Live server state (sizes, users, grants) or null when unavailable.
     *
     * @return array<string, mixed>|null
     */
    public function live(): ?array
    {
        try {
            return (array) $this->agent->call('db.overview');
        } catch (AgentException) {
            return null;
        }
    }
}
