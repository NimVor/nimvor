<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Repositories\PostgresRepository;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Security\Crypto;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * PostgreSQL databases, users and privileges.
 *
 * The agent does the work on the server; this service decides who may ask for what. Every database and user has an
 * owner in the panel's records, and a customer can only act on their own: the ids the browser sends are looked up
 * through the owner scope, never trusted. Names are prefixed with the customer's user name so PostgreSQL's
 * cluster-wide user names cannot collide with, or guess at, another customer's. Passwords are generated here, sent
 * to the agent and shown once; they are only kept (encrypted) when the customer asks.
 */
final class PostgresService
{
    public const LEVELS = ['readonly' => 'Read only', 'readwrite' => 'Read and write', 'all' => 'Full access (also creates tables)'];

    private const NAME = '/^[a-z_][a-z0-9_]{0,62}$/';

    public function __construct(
        private readonly PostgresRepository $repo,
        private readonly WebsiteRepository $sites,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
        private readonly Crypto $crypto,
        private readonly PlanLimits $limits,
    ) {
    }

    /** @return array{installed: bool, running: bool, managed: bool, version: string, port: int} */
    public function status(): array
    {
        $s = (array) $this->agent->call('pg.status');
        return [
            'installed' => !empty($s['installed']), 'running' => !empty($s['running']), 'managed' => !empty($s['managed']),
            'version' => (string) ($s['version'] ?? ''), 'port' => (int) ($s['port'] ?? 5432),
        ];
    }

    public function pgServerId(): int
    {
        return $this->repo->localServerId($this->server->id());
    }

    /** @param array<string, mixed> $actor */
    public function prefix(array $actor): string
    {
        return Scope::isAll($actor) ? '' : substr(preg_replace('/[^a-z0-9_]/', '', strtolower((string) $actor['username'])) ?? '', 0, 16) . '_';
    }

    // ---- servers: the local one and the remote ones an administrator registered ---------------------

    /** Remote servers must be reached encrypted; "require" does not check the certificate, the other two do. */
    public const SSL_MODES = ['require' => 'Encrypted', 'verify-ca' => 'Encrypted, certificate checked against the CA', 'verify-full' => 'Encrypted, certificate and host name checked'];

    /** @return list<array<string, mixed>> */
    public function servers(): array
    {
        $this->pgServerId();
        return $this->repo->servers($this->server->id());
    }

    /** @return list<int> every PostgreSQL server (local and registered) of this managed server */
    public function serverIds(): array
    {
        $this->pgServerId();
        return $this->repo->serverIds($this->server->id());
    }

    /**
     * The connection details the agent needs for a registered server (null for the local one). Built here and only
     * here, from the stored record, and never from anything a browser sent.
     *
     * @return array<string, mixed>|null
     */
    private function targetOf(int $pgServerId): ?array
    {
        $row = $this->repo->serverRow($this->server->id(), $pgServerId);
        if ($row === null) {
            throw new UserFacingException('That PostgreSQL server is not registered.', '', 'Choose one of the listed servers.');
        }
        if ((string) $row['connection_type'] !== 'remote') {
            return null;
        }
        return [
            'host' => (string) $row['host'], 'port' => (int) $row['port'], 'user' => (string) $row['username'],
            'password' => $this->crypto->decrypt((string) $row['credential_enc']),
            'ssl_mode' => (string) $row['ssl_mode'], 'ca_pem' => (string) $row['ca_pem'],
        ];
    }

    /**
     * Ask the agent to act on one of the servers.
     *
     * @param array<string, mixed> $params
     */
    private function callOn(int $pgServerId, string $action, array $params = [], ?int $timeout = null): mixed
    {
        $target = $this->targetOf($pgServerId);
        if ($target !== null) {
            $params['target'] = $target;
        }
        return $this->agent->call($action, $params, $timeout);
    }

    /**
     * Why a host name or address may not be registered, or null when it may. Loopback addresses are this machine
     * (the local server), link-local addresses include the cloud metadata service, and the unspecified and multicast
     * ranges are never a database. Private network addresses are allowed: a database on the same network is the
     * usual case, and only administrators may register servers.
     */
    public static function destinationProblem(string $host): ?string
    {
        $host = trim($host, '[] ');
        if ($host === '' || strlen($host) > 253 || preg_match('/^[A-Za-z0-9.:_-]+$/', $host) !== 1) {
            return 'Enter a host name or an IP address.';
        }
        $ips = filter_var($host, FILTER_VALIDATE_IP) !== false ? [$host] : (gethostbynamel($host) ?: []);
        if (!$ips && filter_var($host, FILTER_VALIDATE_IP) === false) {
            $v6 = @dns_get_record($host, DNS_AAAA);
            foreach ((array) $v6 as $r) {
                $ips[] = (string) ($r['ipv6'] ?? '');
            }
        }
        if (!$ips) {
            return 'The host name does not resolve.';
        }
        foreach ($ips as $ip) {
            $bin = @inet_pton($ip);
            if ($bin === false) {
                return 'Enter a host name or an IP address.';
            }
            $v4 = strlen($bin) === 4 ? $ip : (strlen($bin) === 16 && str_starts_with(bin2hex($bin), '00000000000000000000ffff') ? (string) inet_ntop(substr($bin, 12)) : null);
            if ($v4 !== null && filter_var($v4, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) !== false) {
                $n = ip2long($v4);
                $o = explode('.', $v4);
                if ($n === 0 || (int) $o[0] === 127 || ((int) $o[0] === 169 && (int) $o[1] === 254) || (int) $o[0] === 0 || (int) $o[0] >= 224) {
                    return 'That address is this machine, a link-local or a reserved address. Register a different server; the local server is already managed.';
                }
                continue;
            }
            $hex = bin2hex($bin);
            if ($hex === str_repeat('0', 32) || $hex === str_repeat('0', 31) . '1' || str_starts_with($hex, 'fe8') || str_starts_with($hex, 'fe9') || str_starts_with($hex, 'fea') || str_starts_with($hex, 'feb') || str_starts_with($hex, 'ff')) {
                return 'That address is this machine, a link-local or a reserved address. Register a different server; the local server is already managed.';
            }
        }
        return null;
    }

    /**
     * Register a PostgreSQL server NimVor does not run itself. The connection is tested first and nothing is kept
     * when it fails. The password is stored encrypted and is never shown again.
     *
     * @param array<string, mixed> $input
     */
    public function registerServer(array $input, int $userId): int
    {
        $errors = [];
        $name = trim((string) ($input['name'] ?? ''));
        $host = trim((string) ($input['host'] ?? ''));
        $port = (int) ($input['port'] ?? 5432);
        $user = trim((string) ($input['username'] ?? ''));
        $password = (string) ($input['password'] ?? '');
        $mode = (string) ($input['ssl_mode'] ?? '');
        $ca = trim((string) ($input['ca_pem'] ?? ''));
        if ($name === '' || mb_strlen($name) > 60) {
            $errors['name'] = 'Give the server a name of up to 60 characters.';
        } elseif ($this->repo->serverNameTaken($this->server->id(), $name)) {
            $errors['name'] = 'A server with this name is already registered.';
        }
        if (($problem = self::destinationProblem($host)) !== null) {
            $errors['host'] = $problem;
        }
        if ($port < 1 || $port > 65535) {
            $errors['port'] = 'Enter a port from 1 to 65535.';
        }
        if (preg_match('/^[A-Za-z_][A-Za-z0-9_.@-]{0,62}$/', $user) !== 1) {
            $errors['username'] = 'Enter the PostgreSQL user NimVor manages the server with.';
        }
        if ($password === '' || strlen($password) > 256) {
            $errors['password'] = 'Enter that user\'s password.';
        }
        if (!isset(self::SSL_MODES[$mode])) {
            $errors['ssl_mode'] = 'A remote server must be reached encrypted: choose one of the TLS modes.';
        }
        if ($ca !== '' && (strlen($ca) > 20000 || !str_contains($ca, '-----BEGIN CERTIFICATE-----'))) {
            $errors['ca_pem'] = 'Paste the CA certificate in PEM format (it starts with -----BEGIN CERTIFICATE-----).';
        }
        if (in_array($mode, ['verify-ca'], true) && $ca === '') {
            $errors['ca_pem'] = 'This mode needs the CA certificate that signed the server\'s certificate.';
        }
        if (count($this->repo->servers($this->server->id())) >= 20) {
            $errors['name'] = 'At most 20 servers can be registered.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $target = ['host' => $host, 'port' => $port, 'user' => $user, 'password' => $password, 'ssl_mode' => $mode, 'ca_pem' => $ca];
        try {
            $r = (array) $this->agent->call('pg.test', ['target' => $target], 30);
        } catch (AgentException $e) {
            $this->audit->record('postgres.server_register', 'pg_server', $name, AuditLogger::FAILURE, ['host' => $host, 'port' => $port], $userId);
            throw $e;
        }
        $id = $this->repo->insertServer($this->server->id(), $name, $host, $port, $user, $this->crypto->encrypt($password), $mode, $ca, (string) ($r['version'] ?? ''));
        $this->audit->record('postgres.server_register', 'pg_server', $name, AuditLogger::SUCCESS, ['host' => $host, 'port' => $port, 'ssl_mode' => $mode], $userId);
        return $id;
    }

    /** Test a registered server again and record whether it answered. */
    public function testServer(int $pgServerId, int $userId): string
    {
        $row = $this->repo->serverRow($this->server->id(), $pgServerId);
        if ($row === null || (string) $row['connection_type'] !== 'remote') {
            throw new UserFacingException('That PostgreSQL server is not registered.');
        }
        try {
            $r = (array) $this->callOn($pgServerId, 'pg.test', [], 30);
        } catch (AgentException $e) {
            $this->repo->setServerStatus($pgServerId, 'unreachable', '');
            throw $e;
        }
        $this->repo->setServerStatus($pgServerId, 'active', (string) ($r['version'] ?? ''));
        $this->audit->record('postgres.server_test', 'pg_server', (string) $row['name'], AuditLogger::SUCCESS, [], $userId);
        return (string) ($r['version'] ?? '');
    }

    /** Forget a registered server. It must not hold any database or user NimVor created. */
    public function removeServer(int $pgServerId, int $userId): void
    {
        $row = $this->repo->serverRow($this->server->id(), $pgServerId);
        if ($row === null || (string) $row['connection_type'] !== 'remote') {
            throw new UserFacingException('That PostgreSQL server is not registered.');
        }
        foreach ($this->repo->servers($this->server->id()) as $s) {
            if ((int) $s['id'] === $pgServerId && ((int) $s['database_count'] > 0 || (int) $s['role_count'] > 0)) {
                throw new UserFacingException('This server still has databases or users created through NimVor.', '', 'Delete them first; nothing was removed.');
            }
        }
        $this->repo->deleteServer($pgServerId);
        $this->audit->record('postgres.server_remove', 'pg_server', (string) $row['name'], AuditLogger::SUCCESS, [], $userId);
    }

    // ---- the server (administrators) -------------------------------------------------------------

    public function install(int $userId): string
    {
        $r = (array) $this->agent->call('pg.install', [], 60);
        $this->audit->record('postgres.install', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, [], $userId);
        return (string) ($r['job_id'] ?? '');
    }

    public function adopt(bool $harden, int $userId): void
    {
        $this->agent->call('pg.adopt', ['confirm' => true, 'harden' => $harden], 60);
        $this->audit->record('postgres.adopt', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['harden' => $harden], $userId);
    }

    public function service(string $verb, int $userId): void
    {
        $this->agent->call('pg.service', ['verb' => $verb], 120);
        $this->audit->record('postgres.service', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['verb' => $verb], $userId);
    }

    /**
     * What the server reports (sizes, connections), or null when it cannot be reached.
     *
     * @return array<string, mixed>|null
     */
    public function live(): ?array
    {
        try {
            return (array) $this->agent->call('pg.overview');
        } catch (AgentException) {
            return null;
        }
    }

    /** The server a new database or user goes to: customers always use the local one, administrators may choose. */
    private function chosenServer(array $input, array $actor): int
    {
        $local = $this->pgServerId();
        $want = (int) ($input['pg_server_id'] ?? 0);
        if ($want === 0 || $want === $local) {
            return $local;
        }
        if (!Scope::isAll($actor) || $this->repo->serverRow($this->server->id(), $want) === null) {
            throw new ValidationException(['pg_server_id' => 'Choose one of the listed servers.']);
        }
        return $want;
    }

    // ---- databases and users ---------------------------------------------------------------------

    /**
     * @param array<string, mixed> $input
     * @param array<string, mixed> $actor
     * @return array{database_id: int, user?: string, password?: string}
     */
    public function createDatabase(array $input, array $actor): array
    {
        $pg = $this->chosenServer($input, $actor);
        $prefix = $this->prefix($actor);
        $raw = strtolower(trim((string) ($input['name'] ?? '')));
        $name = $prefix . $raw;
        $siteId = filter_var($input['website_id'] ?? null, FILTER_VALIDATE_INT) ?: null;
        $roleId = filter_var($input['owner_role_id'] ?? null, FILTER_VALIDATE_INT) ?: null;
        $newUser = $roleId === null;
        $errors = [];
        if (!preg_match('/^[a-z0-9_]+$/', $raw) || !preg_match(self::NAME, $name) || $this->reserved($name)) {
            $errors['name'] = 'Use lower-case letters, digits and underscores (up to 63 characters including the prefix ' . ($prefix !== '' ? $prefix : 'none') . ').';
        } elseif ($this->repo->databaseNameTaken($pg, $name)) {
            $errors['name'] = 'A PostgreSQL database with this name already exists.';
        }
        if ($siteId !== null && $this->sites->find($this->server->id(), $siteId, $actor) === null) {
            $errors['website_id'] = 'Choose one of your websites.';
        }
        $role = null;
        $username = '';
        if ($newUser) {
            $rawUser = strtolower(trim((string) ($input['username'] ?? '') ?: (string) ($input['name'] ?? '')));
            $username = $prefix . $rawUser;
            if (!preg_match('/^[a-z0-9_]+$/', $rawUser) || !preg_match(self::NAME, $username) || $this->reserved($username) || $this->repo->roleNameTaken($pg, $username)) {
                $errors['username'] = 'Use lower-case letters, digits and underscores, not already used.';
            }
        } else {
            $role = $this->repo->role($pg, $roleId, $actor);
            if ($role === null) {
                $errors['owner_role_id'] = 'Choose one of your database users.';
            }
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $ownerId = Scope::isAll($actor)
            ? ($siteId !== null ? ($this->sites->find($this->server->id(), $siteId, null)['owner_id'] ?? null) : ($role['owner_id'] ?? null))
            : (int) $actor['id'];
        $ownerId = $ownerId !== null ? (int) $ownerId : null;
        $this->limits->assertCanCreate('databases', $ownerId);

        $out = [];
        $createdRole = false;
        try {
            if ($newUser) {
                $password = Crypto::password();
                $this->callOn($pg, 'pg.role_create', ['name' => $username, 'password' => $password]);
                $createdRole = true;
                $roleId = $this->repo->insertRole($pg, $username, $ownerId, $siteId, !empty($input['store_password']) ? $this->crypto->encrypt($password) : '');
                $this->audit->record('postgres.user_create', 'pg_user', $username, AuditLogger::SUCCESS, ['database' => $name]);
                $out += ['user' => $username, 'password' => $password];
                $ownerRoleName = $username;
            } else {
                $ownerRoleName = (string) $role['name'];
            }
            $this->callOn($pg, 'pg.database_create', ['name' => $name, 'owner' => $ownerRoleName, 'encoding' => (string) ($input['encoding'] ?? 'UTF8') ?: 'UTF8']);
        } catch (AgentException $e) {
            if ($createdRole) {
                // The user was made for this database and nothing uses it: do not leave it behind.
                try {
                    $this->callOn($pg, 'pg.role_delete', ['name' => $username]);
                    $this->repo->deleteRole((int) $roleId);
                } catch (AgentException) {
                }
            }
            throw $e;
        }
        $dbId = $this->repo->insertDatabase($pg, $name, $ownerId, $siteId, (int) $roleId, (string) ($input['encoding'] ?? 'UTF8') ?: 'UTF8');
        $this->audit->record('postgres.database_create', 'pg_database', $name);
        return ['database_id' => $dbId] + $out;
    }

    /**
     * @param array<string, mixed> $input
     * @param array<string, mixed> $actor
     * @return array{user: string, password: string}
     */
    public function createUser(array $input, array $actor): array
    {
        $pg = $this->chosenServer($input, $actor);
        $rawUser = strtolower(trim((string) ($input['username'] ?? '')));
        $username = $this->prefix($actor) . $rawUser;
        if (!preg_match('/^[a-z0-9_]+$/', $rawUser) || !preg_match(self::NAME, $username) || $this->reserved($username) || $this->repo->roleNameTaken($pg, $username)) {
            throw new ValidationException(['username' => 'Use lower-case letters, digits and underscores, not already used.']);
        }
        $siteId = filter_var($input['website_id'] ?? null, FILTER_VALIDATE_INT) ?: null;
        if ($siteId !== null && $this->sites->find($this->server->id(), $siteId, $actor) === null) {
            throw new ValidationException(['website_id' => 'Choose one of your websites.']);
        }
        $ownerId = Scope::isAll($actor)
            ? ($siteId !== null ? ($this->sites->find($this->server->id(), $siteId, null)['owner_id'] ?? null) : null)
            : (int) $actor['id'];
        $password = Crypto::password();
        $this->callOn($pg, 'pg.role_create', ['name' => $username, 'password' => $password]);
        $this->repo->insertRole($pg, $username, $ownerId !== null ? (int) $ownerId : null, $siteId, !empty($input['store_password']) ? $this->crypto->encrypt($password) : '');
        $this->audit->record('postgres.user_create', 'pg_user', $username);
        return ['user' => $username, 'password' => $password];
    }

    /**
     * @param array<string, mixed> $role
     * @return string the new password (shown once)
     */
    public function resetPassword(array $role, bool $store): string
    {
        $password = Crypto::password();
        $this->callOn((int) $role['pg_server_id'], 'pg.role_password', ['name' => (string) $role['name'], 'password' => $password]);
        $this->repo->setPasswordEnc((int) $role['id'], $store ? $this->crypto->encrypt($password) : '');
        $this->audit->record('postgres.user_password', 'pg_user', (string) $role['name']);
        return $password;
    }

    /**
     * Set what a user may do in a database. Both must belong to the same owner, so no one can be given access to
     * another customer's database even by someone who may manage both of their accounts.
     *
     * @param array<string, mixed> $database
     * @param array<string, mixed> $role
     */
    public function setGrant(array $database, array $role, string $level): void
    {
        if ($level !== 'none' && !isset(self::LEVELS[$level])) {
            throw new ValidationException(['level' => 'Choose what the user may do.']);
        }
        if (($database['owner_id'] ?? null) !== ($role['owner_id'] ?? null)) {
            throw new UserFacingException('This user and database belong to different accounts.', '', 'Create a user in the same account as the database.');
        }
        $this->callOn((int) $database['pg_server_id'], 'pg.grant_set', ['database' => (string) $database['name'], 'role' => (string) $role['name'], 'level' => $level]);
        $this->repo->setGrant((int) $database['id'], (int) $role['id'], $level);
        $this->audit->record('postgres.grant', 'pg_database', (string) $database['name'], AuditLogger::SUCCESS, ['user' => $role['name'], 'level' => $level]);
    }

    /** @param array<string, mixed> $database */
    public function dropDatabase(array $database, string $confirm): void
    {
        if ($confirm !== (string) $database['name']) {
            throw new UserFacingException('Type the database name to confirm.', '', 'Nothing was deleted.');
        }
        $this->callOn((int) $database['pg_server_id'], 'pg.database_delete', ['name' => (string) $database['name']]);
        $this->repo->deleteDatabase((int) $database['id']);
        $this->audit->record('postgres.database_delete', 'pg_database', (string) $database['name']);
    }

    /** @param array<string, mixed> $role */
    public function dropUser(array $role): void
    {
        if ($this->repo->ownedBy((int) $role['id']) > 0) {
            throw new UserFacingException('This user owns a database.', '', 'Delete the database first.');
        }
        $this->callOn((int) $role['pg_server_id'], 'pg.role_delete', ['name' => (string) $role['name']]);
        $this->repo->deleteRole((int) $role['id']);
        $this->audit->record('postgres.user_delete', 'pg_user', (string) $role['name']);
    }

    // ---- browsing and editing rows (the database owner's own data) --------------------------------

    /**
     * @param array<string, mixed> $database
     * @return array{schemas: list<string>, tables: list<array<string, mixed>>}
     */
    public function tables(array $database): array
    {
        $o = (array) $this->callOn((int) $database['pg_server_id'], 'pg.tables', ['database' => (string) $database['name']], 60);
        return ['schemas' => array_map('strval', (array) ($o['schemas'] ?? [])), 'tables' => array_map(static fn ($t): array => (array) $t, (array) ($o['tables'] ?? []))];
    }

    /**
     * @param array<string, mixed> $database
     * @return array<string, mixed>
     */
    public function structure(array $database, string $schema, string $table): array
    {
        return (array) $this->callOn((int) $database['pg_server_id'], 'pg.table_structure', ['database' => (string) $database['name'], 'schema' => $schema, 'table' => $table], 60);
    }

    /**
     * One page of a table. $q: offset, limit, sort, desc, search, filters.
     *
     * @param array<string, mixed> $database
     * @param array<string, mixed> $q
     * @return array<string, mixed>
     */
    public function browse(array $database, string $schema, string $table, array $q): array
    {
        return (array) $this->callOn((int) $database['pg_server_id'], 'pg.browse', ['database' => (string) $database['name'], 'schema' => $schema, 'table' => $table] + $q, 60);
    }

    /**
     * Turn the form's per-column choices into the values to write: mode "value" sends the text, "null" sends NULL,
     * anything else (default, keep) leaves the column out.
     *
     * @param array<string, mixed> $modes
     * @param array<string, mixed> $texts
     * @return array<string, string|null>
     */
    public static function values(array $modes, array $texts): array
    {
        $out = [];
        foreach ($modes as $col => $mode) {
            $col = (string) $col;
            if ($mode === 'value') {
                $out[$col] = (string) ($texts[$col] ?? '');
            } elseif ($mode === 'null') {
                $out[$col] = null;
            }
        }
        return $out;
    }

    /** @param array<string, mixed> $database */
    public function auditExport(array $database, string $schema, string $table): void
    {
        $this->audit->record('postgres.export', 'pg_database', (string) $database['name'], AuditLogger::SUCCESS, ['table' => $schema . '.' . $table]);
    }

    /**
     * @param array<string, mixed> $database
     * @param array<string, string|null> $values
     */
    public function insertRow(array $database, string $schema, string $table, array $values): void
    {
        $this->callOn((int) $database['pg_server_id'], 'pg.row_insert', ['database' => (string) $database['name'], 'schema' => $schema, 'table' => $table, 'values' => (object) $values]);
        $this->audit->record('postgres.row_insert', 'pg_database', (string) $database['name'], AuditLogger::SUCCESS, ['table' => $schema . '.' . $table]);
    }

    /**
     * @param array<string, mixed> $database
     * @param array<string, string|null> $key
     * @param array<string, string|null> $values
     */
    public function updateRow(array $database, string $schema, string $table, array $key, array $values): void
    {
        $this->callOn((int) $database['pg_server_id'], 'pg.row_update', ['database' => (string) $database['name'], 'schema' => $schema, 'table' => $table, 'key' => (object) $key, 'values' => (object) $values]);
        $this->audit->record('postgres.row_update', 'pg_database', (string) $database['name'], AuditLogger::SUCCESS, ['table' => $schema . '.' . $table]);
    }

    /**
     * @param array<string, mixed> $database
     * @param array<string, string|null> $key
     */
    public function deleteRow(array $database, string $schema, string $table, array $key): void
    {
        $this->callOn((int) $database['pg_server_id'], 'pg.row_delete', ['database' => (string) $database['name'], 'schema' => $schema, 'table' => $table, 'key' => (object) $key]);
        $this->audit->record('postgres.row_delete', 'pg_database', (string) $database['name'], AuditLogger::SUCCESS, ['table' => $schema . '.' . $table]);
    }

    // ---- SQL editor ----------------------------------------------------------------------------

    /**
     * The statement with its literals replaced by "?", for the history and the audit trail: a statement can hold
     * passwords or personal data, its shape cannot.
     */
    public static function fingerprint(string $sql): string
    {
        $s = preg_replace("/'(?:[^']|'')*'/s", '?', $sql) ?? '';
        $s = preg_replace('/\$([a-zA-Z_]*)\$.*?\$\1\$/s', '?', $s) ?? $s;
        $s = preg_replace('/\b\d+(?:\.\d+)?\b/', '?', $s) ?? $s;
        $s = preg_replace('/\s+/', ' ', trim($s)) ?? '';
        return mb_substr($s, 0, 500);
    }

    /**
     * @param array<string, mixed> $database
     * @return array<string, list<string>> table => columns, for completion
     */
    public function schemaMap(array $database): array
    {
        $o = (array) $this->callOn((int) $database['pg_server_id'], 'pg.schema_map', ['database' => (string) $database['name']], 60);
        $out = [];
        foreach ((array) ($o['schema'] ?? []) as $table => $cols) {
            $out[(string) $table] = array_map('strval', (array) $cols);
        }
        return $out;
    }

    /**
     * Run SQL. Write mode needs the caller to hold databases.manage (the controller checks); the account the
     * statement runs as is chosen by the agent, never by the browser.
     *
     * @param array<string, mixed> $database
     * @param array{mode: string, limit: int, timeout: int, script: bool, explain: string, query_id: string} $opts
     * @return array<string, mixed>
     */
    public function runQuery(array $database, string $sql, array $opts, ?int $userId): array
    {
        $mode = $opts['mode'] === 'write' ? 'write' : 'read';
        $params = ['database' => (string) $database['name'], 'query' => $sql, 'mode' => $mode, 'limit' => $opts['limit'], 'timeout_seconds' => $opts['timeout'],
            'script' => $opts['script'], 'explain' => $opts['explain'], 'query_id' => $opts['query_id']];
        $res = (array) $this->callOn((int) $database['pg_server_id'], 'pg.query', $params, $opts['timeout'] * ($opts['script'] ? 20 : 1) + 30);
        $rows = 0;
        foreach ((array) ($res['results'] ?? []) as $r) {
            $rows += count((array) (((array) $r)['rows'] ?? []));
        }
        $status = isset($res['error']) ? (str_contains(strtolower((string) (((array) $res['error'])['message'] ?? '')), 'cancel') ? 'cancelled' : 'error') : 'ok';
        $fp = self::fingerprint($sql);
        $this->repo->recordQuery((int) $database['id'], $userId, $fp, $mode, (int) ($res['duration_ms'] ?? 0), $rows, $status);
        if ($mode === 'write') {
            $this->audit->record('postgres.query', 'pg_database', (string) $database['name'], $status === 'ok' ? AuditLogger::SUCCESS : AuditLogger::FAILURE, ['mode' => 'write', 'statement' => $fp], $userId);
        }
        return $res;
    }

    /** @param array<string, mixed> $database */
    public function cancelQuery(array $database, string $queryId): bool
    {
        $r = (array) $this->callOn((int) $database['pg_server_id'], 'pg.query_cancel', ['database' => (string) $database['name'], 'query_id' => $queryId]);
        return !empty($r['cancelled']);
    }

    /**
     * @param array<string, mixed> $database
     * @return list<array<string, mixed>>
     */
    public function history(array $database, ?int $userId): array
    {
        return $this->repo->history((int) $database['id'], $userId);
    }

    // ---- backups and monitoring ----------------------------------------------------------------

    /** @param array<string, mixed> $database */
    private function backupFileOk(array $database, string $file): void
    {
        if (preg_match('/^\d{8}-\d{6}_([a-z_][a-z0-9_]{0,62})\.dump$/', $file, $m) !== 1 || $m[1] !== (string) $database['name']) {
            throw new UserFacingException('That is not a backup of this database.');
        }
    }

    /**
     * @param array<string, mixed> $database
     * @return list<array{file: string, size: int, created: string, sha256: string}>
     */
    public function backups(array $database): array
    {
        $o = (array) $this->callOn((int) $database['pg_server_id'], 'pg.backup_list', ['database' => (string) $database['name']]);
        return array_map(static fn ($b): array => [
            'file' => (string) ($b['file'] ?? ''), 'size' => (int) ($b['size'] ?? 0),
            'created' => (string) ($b['created'] ?? ''), 'sha256' => (string) ($b['sha256'] ?? ''),
        ], (array) ($o['backups'] ?? []));
    }

    /** @param array<string, mixed> $database  @return string the job id */
    public function createBackup(array $database, ?int $userId): string
    {
        $o = (array) $this->callOn((int) $database['pg_server_id'], 'pg.backup_create', ['database' => (string) $database['name']], 60);
        $this->audit->record('postgres.backup', 'pg_database', (string) $database['name'], AuditLogger::SUCCESS, [], $userId);
        return (string) ($o['job_id'] ?? '');
    }

    /** @param array<string, mixed> $database  @return string the job id */
    public function restoreBackup(array $database, string $file, ?int $userId): string
    {
        $this->backupFileOk($database, $file);
        $o = (array) $this->callOn((int) $database['pg_server_id'], 'pg.backup_restore', ['database' => (string) $database['name'], 'file' => $file], 60);
        $this->audit->record('postgres.restore', 'pg_database', (string) $database['name'], AuditLogger::SUCCESS, ['file' => $file], $userId);
        return (string) ($o['job_id'] ?? '');
    }

    /** @param array<string, mixed> $database */
    public function deleteBackup(array $database, string $file, ?int $userId): void
    {
        $this->backupFileOk($database, $file);
        $this->callOn((int) $database['pg_server_id'], 'pg.backup_delete', ['database' => (string) $database['name'], 'file' => $file]);
        $this->audit->record('postgres.backup_delete', 'pg_database', (string) $database['name'], AuditLogger::SUCCESS, ['file' => $file], $userId);
    }

    /** @param array<string, mixed> $database  @return array<string, mixed>|null */
    public function schedule(array $database): ?array
    {
        return $this->repo->schedule((int) $database['id']);
    }

    /** @param array<string, mixed> $database */
    public function saveSchedule(array $database, string $frequency, int $hour, int $keep, ?int $userId): void
    {
        if (!in_array($frequency, ['daily', 'weekly'], true) || $hour < 0 || $hour > 23 || $keep < 1 || $keep > 90) {
            throw new ValidationException(['schedule' => 'Choose daily or weekly, an hour from 0 to 23 and between 1 and 90 backups to keep.']);
        }
        $this->repo->saveSchedule((int) $database['id'], $frequency, $hour, $keep);
        $this->audit->record('postgres.backup_schedule', 'pg_database', (string) $database['name'], AuditLogger::SUCCESS, ['frequency' => $frequency, 'hour' => $hour, 'keep' => $keep], $userId);
    }

    /** @param array<string, mixed> $database */
    public function deleteSchedule(array $database, ?int $userId): void
    {
        $this->repo->deleteSchedule((int) $database['id']);
        $this->audit->record('postgres.backup_schedule_delete', 'pg_database', (string) $database['name'], AuditLogger::SUCCESS, [], $userId);
    }

    /** The most recent moment a schedule was due (UTC). */
    public static function lastSlot(string $frequency, int $hour, int $now): int
    {
        $slot = gmmktime($hour, 0, 0, (int) gmdate('n', $now), (int) gmdate('j', $now), (int) gmdate('Y', $now));
        if ($slot > $now) {
            $slot -= 86400;
        }
        if ($frequency === 'weekly') {
            while ((int) gmdate('w', $slot) !== 0) {
                $slot -= 86400;
            }
        }
        return $slot;
    }

    /**
     * Start the backups that are due (called every few minutes by `schedule:run`). The older ones beyond the number to
     * keep are removed first, so a database holds at most "keep" + 1 archives until the next pass.
     *
     * @return list<string> log lines
     */
    public function runDueBackups(?int $now = null): array
    {
        $now ??= time();
        $lines = [];
        $localOk = false;
        try {
            $localOk = !empty($this->status()['managed']);
        } catch (AgentException) {
        }
        $local = $this->pgServerId();
        foreach ($this->repo->schedulesOf($this->serverIds()) as $s) {
            if ((int) $s['pg_server_id'] === $local && !$localOk) {
                continue;
            }
            $slot = self::lastSlot((string) $s['frequency'], (int) $s['hour'], $now);
            $last = $s['last_run_at'] !== null ? (int) strtotime((string) $s['last_run_at']) : 0;
            if ($last >= $slot) {
                continue;
            }
            $name = (string) $s['database_name'];
            try {
                $this->callOn((int) $s['pg_server_id'], 'pg.backup_prune', ['database' => $name, 'keep' => (int) $s['keep']]);
                $this->callOn((int) $s['pg_server_id'], 'pg.backup_create', ['database' => $name], 60);
                $this->repo->markScheduleRun((int) $s['id'], 'started', $now);
                $this->audit->record('postgres.backup', 'pg_database', $name, AuditLogger::SUCCESS, ['scheduled' => true], null);
                $lines[] = 'PostgreSQL backup of ' . $name . ' started.';
            } catch (AgentException | UserFacingException $e) {
                $this->repo->markScheduleRun((int) $s['id'], 'failed', $now);
                $this->audit->record('postgres.backup', 'pg_database', $name, AuditLogger::FAILURE, ['scheduled' => true], null);
                $lines[] = 'PostgreSQL backup of ' . $name . ' failed: ' . $e->getMessage();
            }
        }
        return $lines;
    }

    /** @return array<string, mixed> */
    public function monitor(?int $pgServerId = null): array
    {
        return (array) $this->callOn($pgServerId ?? $this->pgServerId(), 'pg.monitor', [], 30);
    }

    private function reserved(string $name): bool
    {
        return in_array($name, ['postgres', 'template0', 'template1', 'public', 'none', 'nimvor_admin', 'replication'], true)
            || str_starts_with($name, 'pg_') || str_starts_with($name, 'nimvor_');
    }
}
