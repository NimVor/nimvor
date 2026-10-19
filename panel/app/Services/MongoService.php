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

    /** Insert one document (EJSON). Returns the inserted _id (EJSON). */
    public function insertDocument(string $db, string $collection, string $document, ?int $userId): array
    {
        $o = (array) $this->agent->call('mongo.document_insert', ['db' => $db, 'collection' => $collection, 'document' => $document]);
        $this->audit->record('mongo.document_insert', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection], $userId);
        return ['inserted_id' => $o['inserted_id'] ?? null];
    }

    /** Replace the document with this _id (EJSON) by a new document (EJSON). */
    public function updateDocument(string $db, string $collection, string $id, string $document, ?int $userId): array
    {
        $o = (array) $this->agent->call('mongo.document_update', ['db' => $db, 'collection' => $collection, 'id' => $id, 'document' => $document]);
        $this->audit->record('mongo.document_update', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection], $userId);
        return ['matched' => (int) ($o['matched'] ?? 0), 'modified' => (int) ($o['modified'] ?? 0)];
    }

    /** Run an aggregation pipeline (EJSON array). @return array{documents: mixed, ms: int, limit: int} */
    public function aggregate(string $db, string $collection, string $pipeline, int $limit): array
    {
        $o = (array) $this->agent->call('mongo.aggregate', ['db' => $db, 'collection' => $collection, 'pipeline' => $pipeline, 'limit' => $limit], 90);
        return ['documents' => $o['documents'] ?? [], 'ms' => (int) ($o['ms'] ?? 0), 'limit' => (int) ($o['limit'] ?? $limit)];
    }

    /**
     * Export documents of a collection as a JSON array or JSONL (bounded).
     * The filter is a JSON object (EJSON).
     *
     * @return array{content: string, count: int, format: string}
     */
    public function export(string $db, string $collection, string $format, string $filter, int $limit, ?int $userId): array
    {
        $o = (array) $this->agent->call('mongo.export', ['db' => $db, 'collection' => $collection, 'format' => $format, 'filter' => $filter, 'limit' => $limit], 120);
        $this->audit->record('mongo.export', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection, 'format' => $format], $userId);
        return ['content' => (string) ($o['content'] ?? ''), 'count' => (int) ($o['count'] ?? 0), 'format' => (string) ($o['format'] ?? $format)];
    }

    /**
     * Import documents from a JSON array or JSONL into a collection (bounded).
     *
     * @return array{inserted: int, errors: int, total: int}
     */
    public function import(string $db, string $collection, string $format, string $content, ?int $userId): array
    {
        $o = (array) $this->agent->call('mongo.import', ['db' => $db, 'collection' => $collection, 'format' => $format, 'content' => $content], 120);
        $this->audit->record('mongo.import', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection, 'format' => $format], $userId);
        return ['inserted' => (int) ($o['inserted'] ?? 0), 'errors' => (int) ($o['errors'] ?? 0), 'total' => (int) ($o['total'] ?? 0)];
    }

    /**
     * Analyse a collection's top-level fields over a bounded sample.
     *
     * @return array{sampled: int, fields: mixed}
     */
    public function schema(string $db, string $collection, int $sample): array
    {
        $o = (array) $this->agent->call('mongo.schema', ['db' => $db, 'collection' => $collection, 'sample' => $sample], 90);
        return ['sampled' => (int) ($o['sampled'] ?? 0), 'fields' => $o['fields'] ?? []];
    }

    // --- Indexes ---

    /** @return array{indexes: mixed} */
    public function indexes(string $db, string $collection): array
    {
        $o = (array) $this->agent->call('mongo.indexes', ['db' => $db, 'collection' => $collection]);
        return ['indexes' => $o['indexes'] ?? []];
    }

    public function createIndex(string $db, string $collection, string $keys, string $options, ?int $userId): string
    {
        $o = (array) $this->agent->call('mongo.index_create', ['db' => $db, 'collection' => $collection, 'keys' => $keys, 'options' => $options], 300);
        $this->audit->record('mongo.index_create', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection, 'keys' => $keys], $userId);
        return (string) ($o['name'] ?? '');
    }

    public function dropIndex(string $db, string $collection, string $name, ?int $userId): void
    {
        $this->agent->call('mongo.index_drop', ['db' => $db, 'collection' => $collection, 'name' => $name]);
        $this->audit->record('mongo.index_drop', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection, 'name' => $name], $userId);
    }

    // --- Configuration and log ---

    /** @return array{path: string, port: int, bind_ip: string, db_path: string, log_path: string, authorization: bool, raw: string} */
    public function config(): array
    {
        $c = (array) $this->agent->call('mongo.config_get');
        return [
            'path' => (string) ($c['path'] ?? '/etc/mongod.conf'),
            'port' => (int) ($c['port'] ?? 27017),
            'bind_ip' => (string) ($c['bind_ip'] ?? '127.0.0.1'),
            'db_path' => (string) ($c['db_path'] ?? ''),
            'log_path' => (string) ($c['log_path'] ?? ''),
            'authorization' => (bool) ($c['authorization'] ?? false),
            'raw' => (string) ($c['raw'] ?? ''),
        ];
    }

    public function setConfig(int $port, string $bindIp, bool $authorization, ?int $userId): void
    {
        $this->agent->call('mongo.config_set', ['port' => $port, 'bind_ip' => $bindIp, 'authorization' => $authorization], 120);
        $this->audit->record('mongo.config_set', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['port' => $port, 'bind_ip' => $bindIp, 'authorization' => $authorization], $userId);
    }

    /** @return array{path: string, lines: string} */
    public function log(int $lines = 300): array
    {
        $l = (array) $this->agent->call('mongo.log', ['lines' => $lines]);
        return ['path' => (string) ($l['path'] ?? ''), 'lines' => (string) ($l['lines'] ?? '')];
    }
}
