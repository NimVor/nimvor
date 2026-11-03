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

    // ---- databases and users ---------------------------------------------------------------------

    /**
     * @param array<string, mixed> $input
     * @param array<string, mixed> $actor
     * @return array{database_id: int, user?: string, password?: string}
     */
    public function createDatabase(array $input, array $actor): array
    {
        $pg = $this->pgServerId();
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
                $this->agent->call('pg.role_create', ['name' => $username, 'password' => $password]);
                $createdRole = true;
                $roleId = $this->repo->insertRole($pg, $username, $ownerId, $siteId, !empty($input['store_password']) ? $this->crypto->encrypt($password) : '');
                $this->audit->record('postgres.user_create', 'pg_user', $username, AuditLogger::SUCCESS, ['database' => $name]);
                $out += ['user' => $username, 'password' => $password];
                $ownerRoleName = $username;
            } else {
                $ownerRoleName = (string) $role['name'];
            }
            $this->agent->call('pg.database_create', ['name' => $name, 'owner' => $ownerRoleName, 'encoding' => (string) ($input['encoding'] ?? 'UTF8') ?: 'UTF8']);
        } catch (AgentException $e) {
            if ($createdRole) {
                // The user was made for this database and nothing uses it: do not leave it behind.
                try {
                    $this->agent->call('pg.role_delete', ['name' => $username]);
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
        $pg = $this->pgServerId();
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
        $this->agent->call('pg.role_create', ['name' => $username, 'password' => $password]);
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
        $this->agent->call('pg.role_password', ['name' => (string) $role['name'], 'password' => $password]);
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
        $this->agent->call('pg.grant_set', ['database' => (string) $database['name'], 'role' => (string) $role['name'], 'level' => $level]);
        $this->repo->setGrant((int) $database['id'], (int) $role['id'], $level);
        $this->audit->record('postgres.grant', 'pg_database', (string) $database['name'], AuditLogger::SUCCESS, ['user' => $role['name'], 'level' => $level]);
    }

    /** @param array<string, mixed> $database */
    public function dropDatabase(array $database, string $confirm): void
    {
        if ($confirm !== (string) $database['name']) {
            throw new UserFacingException('Type the database name to confirm.', '', 'Nothing was deleted.');
        }
        $this->agent->call('pg.database_delete', ['name' => (string) $database['name']]);
        $this->repo->deleteDatabase((int) $database['id']);
        $this->audit->record('postgres.database_delete', 'pg_database', (string) $database['name']);
    }

    /** @param array<string, mixed> $role */
    public function dropUser(array $role): void
    {
        if ($this->repo->ownedBy((int) $role['id']) > 0) {
            throw new UserFacingException('This user owns a database.', '', 'Delete the database first.');
        }
        $this->agent->call('pg.role_delete', ['name' => (string) $role['name']]);
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
        $o = (array) $this->agent->call('pg.tables', ['database' => (string) $database['name']], 60);
        return ['schemas' => array_map('strval', (array) ($o['schemas'] ?? [])), 'tables' => array_map(static fn ($t): array => (array) $t, (array) ($o['tables'] ?? []))];
    }

    /**
     * @param array<string, mixed> $database
     * @return array<string, mixed>
     */
    public function structure(array $database, string $schema, string $table): array
    {
        return (array) $this->agent->call('pg.table_structure', ['database' => (string) $database['name'], 'schema' => $schema, 'table' => $table], 60);
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
        return (array) $this->agent->call('pg.browse', ['database' => (string) $database['name'], 'schema' => $schema, 'table' => $table] + $q, 60);
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
        $this->agent->call('pg.row_insert', ['database' => (string) $database['name'], 'schema' => $schema, 'table' => $table, 'values' => (object) $values]);
        $this->audit->record('postgres.row_insert', 'pg_database', (string) $database['name'], AuditLogger::SUCCESS, ['table' => $schema . '.' . $table]);
    }

    /**
     * @param array<string, mixed> $database
     * @param array<string, string|null> $key
     * @param array<string, string|null> $values
     */
    public function updateRow(array $database, string $schema, string $table, array $key, array $values): void
    {
        $this->agent->call('pg.row_update', ['database' => (string) $database['name'], 'schema' => $schema, 'table' => $table, 'key' => (object) $key, 'values' => (object) $values]);
        $this->audit->record('postgres.row_update', 'pg_database', (string) $database['name'], AuditLogger::SUCCESS, ['table' => $schema . '.' . $table]);
    }

    /**
     * @param array<string, mixed> $database
     * @param array<string, string|null> $key
     */
    public function deleteRow(array $database, string $schema, string $table, array $key): void
    {
        $this->agent->call('pg.row_delete', ['database' => (string) $database['name'], 'schema' => $schema, 'table' => $table, 'key' => (object) $key]);
        $this->audit->record('postgres.row_delete', 'pg_database', (string) $database['name'], AuditLogger::SUCCESS, ['table' => $schema . '.' . $table]);
    }

    private function reserved(string $name): bool
    {
        return in_array($name, ['postgres', 'template0', 'template1', 'public', 'none', 'nimvor_admin', 'replication'], true)
            || str_starts_with($name, 'pg_') || str_starts_with($name, 'nimvor_');
    }
}
