<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;

/**
 * MongoDB server management: databases and users, through the agent (mongosh).
 */
final class MongoService
{
    public function __construct(
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @return array{installed: bool, running: bool, version: string, auth: bool} */
    public function status(): array
    {
        $s = (array) $this->agent->call('mongo.status');
        return [
            'installed' => !empty($s['installed']),
            'running' => !empty($s['running']),
            'version' => (string) ($s['version'] ?? ''),
            'auth' => !empty($s['auth']),
        ];
    }

    /** @return array{kind: string, name: string, url: string, version: string, supported: bool, enabled: bool, note: string} */
    public function repository(): array
    {
        $r = (array) $this->agent->call('mongo.repository');
        return [
            'kind' => (string) ($r['kind'] ?? ''),
            'name' => (string) ($r['name'] ?? ''),
            'url' => (string) ($r['url'] ?? ''),
            'version' => (string) ($r['version'] ?? ''),
            'supported' => !empty($r['supported']),
            'enabled' => !empty($r['enabled']),
            'note' => (string) ($r['note'] ?? ''),
        ];
    }

    /** Add MongoDB's repository (async). Returns the job id. */
    public function enableRepository(?int $userId): string
    {
        $job = (string) ((array) $this->agent->call('mongo.repository.enable'))['job_id'];
        $this->audit->record('mongo.repository.enable', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, [], $userId);
        return $job;
    }

    /** Install MongoDB and start the service (async). Returns the job id. */
    public function install(?int $userId): string
    {
        $job = (string) ((array) $this->agent->call('mongo.install'))['job_id'];
        $this->audit->record('mongo.install', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, [], $userId);
        return $job;
    }

    /** Start, stop, restart or enable the mongod service. */
    public function service(string $op, ?int $userId): void
    {
        $this->agent->call('services.control', ['unit' => 'mongod.service', 'op' => $op]);
        $this->audit->record('service.' . $op, 'service', 'mongod', AuditLogger::SUCCESS, [], $userId);
    }

    /** @return array{databases: list<array<string, mixed>>, users: list<array<string, mixed>>, databases_error: string, users_error: string} */
    public function overview(): array
    {
        $o = (array) $this->agent->call('mongo.overview');
        return [
            'databases' => array_map(static fn ($d): array => (array) $d, (array) ($o['databases'] ?? [])),
            'users' => array_map(static fn ($u): array => (array) $u, (array) ($o['users'] ?? [])),
            'databases_error' => (string) ($o['databases_error'] ?? ''),
            'users_error' => (string) ($o['users_error'] ?? ''),
        ];
    }

    public function createDatabase(string $name, ?int $userId): void
    {
        $this->agent->call('mongo.database_create', ['name' => $name]);
        $this->audit->record('mongo.database_create', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['name' => $name], $userId);
    }

    public function dropDatabase(string $name, string $confirm, ?int $userId): void
    {
        $this->agent->call('mongo.database_drop', ['name' => $name, 'confirm' => $confirm]);
        $this->audit->record('mongo.database_drop', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['name' => $name], $userId);
    }

    public function createUser(string $user, string $password, string $db, string $role, ?int $userId): void
    {
        $this->agent->call('mongo.user_create', ['user' => $user, 'password' => $password, 'db' => $db, 'role' => $role]);
        $this->audit->record('mongo.user_create', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['user' => $user, 'db' => $db, 'role' => $role], $userId);
    }

    public function setPassword(string $user, string $db, string $password, ?int $userId): void
    {
        $this->agent->call('mongo.user_password', ['user' => $user, 'db' => $db, 'password' => $password]);
        $this->audit->record('mongo.user_password', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['user' => $user, 'db' => $db], $userId);
    }

    public function dropUser(string $user, string $db, ?int $userId): void
    {
        $this->agent->call('mongo.user_drop', ['user' => $user, 'db' => $db]);
        $this->audit->record('mongo.user_drop', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['user' => $user, 'db' => $db], $userId);
    }

    // --- Database browser ---

    /** @return list<array{name: string, count: int}> */
    public function collections(string $db): array
    {
        $o = (array) $this->agent->call('mongo.collections', ['db' => $db]);
        return array_map(static fn ($c): array => ['name' => (string) ($c['name'] ?? ''), 'count' => (int) ($c['count'] ?? 0)], (array) ($o['collections'] ?? []));
    }

    /**
     * Find documents in a collection. The filter is a JSON object (EJSON).
     *
     * @return array{documents: mixed, total: int, skip: int, limit: int}
     */
    public function documents(string $db, string $collection, string $filter, int $skip, int $limit): array
    {
        $o = (array) $this->agent->call('mongo.documents', ['db' => $db, 'collection' => $collection, 'filter' => $filter, 'skip' => $skip, 'limit' => $limit], 90);
        return [
            'documents' => $o['documents'] ?? [],
            'total' => (int) ($o['total'] ?? 0),
            'skip' => (int) ($o['skip'] ?? $skip),
            'limit' => (int) ($o['limit'] ?? $limit),
        ];
    }

    /** Delete one document by its _id (EJSON value). Returns the number deleted. */
    public function deleteDocument(string $db, string $collection, string $id, ?int $userId): int
    {
        $o = (array) $this->agent->call('mongo.document_delete', ['db' => $db, 'collection' => $collection, 'id' => $id]);
        $this->audit->record('mongo.document_delete', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection], $userId);
        return (int) ($o['deleted'] ?? 0);
    }
}
