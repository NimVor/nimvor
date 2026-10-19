<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Support\UserFacingException;

/**
 * Per-website MongoDB for the customer portal. Databases and users are
 * prefixed with the website's system account ("<user>_"), the same way MySQL
 * databases are, so each customer sees and manages only their own. The drop
 * and password actions refuse any name that is not under the account's prefix,
 * so one customer can never touch another's MongoDB database or user even by
 * passing a crafted name.
 */
final class SiteMongoService
{
    public function __construct(
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @param array<string, mixed> $site */
    public function prefix(array $site): string
    {
        return strtolower((string) $site['system_user']) . '_';
    }

    /** @return array{installed: bool, running: bool, version: string} */
    public function status(): array
    {
        $s = (array) $this->agent->call('mongo.status');
        return ['installed' => !empty($s['installed']), 'running' => !empty($s['running']), 'version' => (string) ($s['version'] ?? '')];
    }

    /**
     * The customer's own databases and users (filtered to their prefix).
     *
     * @param array<string, mixed> $site
     * @return array{databases: list<array<string, mixed>>, users: list<array<string, mixed>>}
     */
    public function overview(array $site): array
    {
        $prefix = $this->prefix($site);
        $o = (array) $this->agent->call('mongo.overview');
        $mine = static fn (string $name): bool => str_starts_with(strtolower($name), $prefix);
        $dbs = array_values(array_filter(array_map(static fn ($d): array => (array) $d, (array) ($o['databases'] ?? [])), static fn (array $d): bool => $mine((string) ($d['name'] ?? ''))));
        $users = array_values(array_filter(array_map(static fn ($u): array => (array) $u, (array) ($o['users'] ?? [])), static fn (array $u): bool => $mine((string) ($u['db'] ?? ''))));
        return ['databases' => $dbs, 'users' => $users];
    }

    /** @param array<string, mixed> $site */
    public function createDatabase(array $site, string $name, ?int $userId): string
    {
        $full = $this->scoped($site, $name);
        $this->agent->call('mongo.database_create', ['name' => $full]);
        $this->audit->record('mongo.database_create', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['name' => $full], $userId);
        return $full;
    }

    /** @param array<string, mixed> $site */
    public function dropDatabase(array $site, string $name, ?int $userId): void
    {
        $this->assertOwned($site, $name);
        $this->agent->call('mongo.database_drop', ['name' => $name, 'confirm' => $name]);
        $this->audit->record('mongo.database_drop', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['name' => $name], $userId);
    }

    /** @param array<string, mixed> $site */
    public function createUser(array $site, string $user, string $password, string $db, string $role, ?int $userId): string
    {
        $this->assertOwned($site, $db);
        $fullUser = $this->scoped($site, $user);
        $this->agent->call('mongo.user_create', ['user' => $fullUser, 'password' => $password, 'db' => $db, 'role' => $role]);
        $this->audit->record('mongo.user_create', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['user' => $fullUser, 'db' => $db, 'role' => $role], $userId);
        return $fullUser;
    }

    /** @param array<string, mixed> $site */
    public function setPassword(array $site, string $user, string $db, string $password, ?int $userId): void
    {
        $this->assertOwned($site, $db);
        $this->assertOwned($site, $user);
        $this->agent->call('mongo.user_password', ['user' => $user, 'db' => $db, 'password' => $password]);
        $this->audit->record('mongo.user_password', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['user' => $user, 'db' => $db], $userId);
    }

    /** @param array<string, mixed> $site */
    public function dropUser(array $site, string $user, string $db, ?int $userId): void
    {
        $this->assertOwned($site, $db);
        $this->assertOwned($site, $user);
        $this->agent->call('mongo.user_drop', ['user' => $user, 'db' => $db]);
        $this->audit->record('mongo.user_drop', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['user' => $user, 'db' => $db], $userId);
    }

    /**
     * The users defined on one of the account's own databases.
     *
     * @param array<string, mixed> $site
     * @return list<array{user: string, roles: list<string>}>
     */
    public function databaseUsers(array $site, string $db): array
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.database_users', ['db' => $db]);
        return array_map(
            static fn ($u): array => ['user' => (string) ($u['user'] ?? ''), 'roles' => array_map('strval', (array) ($u['roles'] ?? []))],
            (array) ($o['users'] ?? []),
        );
    }

    // --- Backup and restore (confined to the account's own databases) ---

    /** Start a backup of one of the account's own databases. Returns the job id and archive name. */
    public function backup(array $site, string $db, ?int $userId): array
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.backup', ['db' => $db]);
        $this->audit->record('mongo.backup', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['db' => $db], $userId);
        return ['job_id' => (string) ($o['job_id'] ?? ''), 'file' => (string) ($o['file'] ?? '')];
    }

    /**
     * The account's own backup archives (filtered to its prefix).
     *
     * @param array<string, mixed> $site
     * @return list<array{file: string, db: string, size: int, created: string}>
     */
    public function backups(array $site): array
    {
        $prefix = $this->prefix($site);
        $o = (array) $this->agent->call('mongo.backups');
        $all = array_map(static fn ($b): array => [
            'file' => (string) ($b['file'] ?? ''),
            'db' => (string) ($b['db'] ?? ''),
            'size' => (int) ($b['size'] ?? 0),
            'created' => (string) ($b['created'] ?? ''),
        ], (array) ($o['backups'] ?? []));
        return array_values(array_filter($all, static fn (array $b): bool => str_starts_with(strtolower($b['db']), $prefix)));
    }

    /** Restore one of the account's own databases from one of its archives. Returns the job id. */
    public function restore(array $site, string $file, bool $drop, ?int $userId): string
    {
        $db = $this->backupDb($site, $file);
        $o = (array) $this->agent->call('mongo.restore', ['file' => $file, 'db' => $db, 'drop' => $drop]);
        $this->audit->record('mongo.restore', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['db' => $db, 'file' => $file, 'drop' => $drop], $userId);
        return (string) ($o['job_id'] ?? '');
    }

    public function deleteBackup(array $site, string $file, ?int $userId): void
    {
        $this->backupDb($site, $file);
        $this->agent->call('mongo.backup_delete', ['file' => $file]);
        $this->audit->record('mongo.backup_delete', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['file' => $file], $userId);
    }

    /**
     * Validate a backup archive name and return the database it holds, after
     * checking that database belongs to this account. The name is
     * "<timestamp>_<db>.archive.gz": the timestamp is a fixed 15 characters, so
     * the database (which may contain underscores) is everything from the first
     * underscore to the ".archive.gz" suffix.
     *
     * @param array<string, mixed> $site
     */
    private function backupDb(array $site, string $file): string
    {
        if (preg_match('/^\d{8}-\d{6}_([A-Za-z0-9_][A-Za-z0-9_.-]{0,80})\.archive\.gz$/', $file, $m) !== 1) {
            throw new UserFacingException('That is not a backup archive.');
        }
        $this->assertOwned($site, $m[1]);
        return $m[1];
    }

    // --- Database browser (confined to the account's own databases) ---

    /**
     * Collections in one of the account's own databases.
     *
     * @param array<string, mixed> $site
     * @return list<array{name: string, count: int}>
     */
    public function collections(array $site, string $db): array
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.collections', ['db' => $db]);
        return array_map(static fn ($c): array => ['name' => (string) ($c['name'] ?? ''), 'count' => (int) ($c['count'] ?? 0)], (array) ($o['collections'] ?? []));
    }

    public function createCollection(array $site, string $db, string $collection, ?int $userId): void
    {
        $this->assertOwned($site, $db);
        $this->agent->call('mongo.collection_create', ['db' => $db, 'collection' => $collection]);
        $this->audit->record('mongo.collection_create', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection], $userId);
    }

    public function dropCollection(array $site, string $db, string $collection, ?int $userId): void
    {
        $this->assertOwned($site, $db);
        $this->agent->call('mongo.collection_drop', ['db' => $db, 'collection' => $collection]);
        $this->audit->record('mongo.collection_drop', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection], $userId);
    }

    public function emptyCollection(array $site, string $db, string $collection, ?int $userId): int
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.collection_empty', ['db' => $db, 'collection' => $collection], 90);
        $this->audit->record('mongo.collection_empty', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection], $userId);
        return (int) ($o['deleted'] ?? 0);
    }

    public function renameCollection(array $site, string $db, string $collection, string $name, ?int $userId): void
    {
        $this->assertOwned($site, $db);
        $this->agent->call('mongo.collection_rename', ['db' => $db, 'collection' => $collection, 'name' => $name]);
        $this->audit->record('mongo.collection_rename', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection, 'name' => $name], $userId);
    }

    /**
     * Documents in a collection of one of the account's own databases.
     *
     * @param array<string, mixed> $site
     * @return array{documents: mixed, total: int, skip: int, limit: int}
     */
    public function documents(array $site, string $db, string $collection, string $filter, int $skip, int $limit, string $sort = '', string $projection = ''): array
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.documents', ['db' => $db, 'collection' => $collection, 'filter' => $filter, 'sort' => $sort, 'projection' => $projection, 'skip' => $skip, 'limit' => $limit], 90);
        return [
            'documents' => $o['documents'] ?? [],
            'total' => (int) ($o['total'] ?? 0),
            'skip' => (int) ($o['skip'] ?? $skip),
            'limit' => (int) ($o['limit'] ?? $limit),
        ];
    }

    /** Delete one document by its _id (EJSON), only in the account's own database. */
    public function deleteDocument(array $site, string $db, string $collection, string $id, ?int $userId): int
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.document_delete', ['db' => $db, 'collection' => $collection, 'id' => $id]);
        $this->audit->record('mongo.document_delete', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection], $userId);
        return (int) ($o['deleted'] ?? 0);
    }

    /** Insert one document (EJSON) into the account's own database. */
    public function insertDocument(array $site, string $db, string $collection, string $document, ?int $userId): array
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.document_insert', ['db' => $db, 'collection' => $collection, 'document' => $document]);
        $this->audit->record('mongo.document_insert', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection], $userId);
        return ['inserted_id' => $o['inserted_id'] ?? null];
    }

    /** Replace a document (found by _id EJSON) in the account's own database. */
    public function updateDocument(array $site, string $db, string $collection, string $id, string $document, ?int $userId): array
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.document_update', ['db' => $db, 'collection' => $collection, 'id' => $id, 'document' => $document]);
        $this->audit->record('mongo.document_update', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection], $userId);
        return ['matched' => (int) ($o['matched'] ?? 0), 'modified' => (int) ($o['modified'] ?? 0)];
    }

    /** Run an aggregation pipeline in the account's own database. @return array{documents: mixed, ms: int, limit: int} */
    public function aggregate(array $site, string $db, string $collection, string $pipeline, int $limit): array
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.aggregate', ['db' => $db, 'collection' => $collection, 'pipeline' => $pipeline, 'limit' => $limit], 90);
        return ['documents' => $o['documents'] ?? [], 'ms' => (int) ($o['ms'] ?? 0), 'limit' => (int) ($o['limit'] ?? $limit)];
    }

    /**
     * Export documents of a collection in the account's own database as a JSON
     * array or JSONL (bounded). @return array{content: string, count: int, format: string}
     */
    public function export(array $site, string $db, string $collection, string $format, string $filter, int $limit, ?int $userId): array
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.export', ['db' => $db, 'collection' => $collection, 'format' => $format, 'filter' => $filter, 'limit' => $limit], 120);
        $this->audit->record('mongo.export', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection, 'format' => $format], $userId);
        return ['content' => (string) ($o['content'] ?? ''), 'count' => (int) ($o['count'] ?? 0), 'format' => (string) ($o['format'] ?? $format)];
    }

    /**
     * Import documents into a collection of the account's own database.
     *
     * @return array{inserted: int, errors: int, total: int}
     */
    public function import(array $site, string $db, string $collection, string $format, string $content, ?int $userId): array
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.import', ['db' => $db, 'collection' => $collection, 'format' => $format, 'content' => $content], 120);
        $this->audit->record('mongo.import', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection, 'format' => $format], $userId);
        return ['inserted' => (int) ($o['inserted'] ?? 0), 'errors' => (int) ($o['errors'] ?? 0), 'total' => (int) ($o['total'] ?? 0)];
    }

    /**
     * Analyse a collection of the account's own database over a bounded sample.
     *
     * @return array{sampled: int, fields: mixed}
     */
    public function schema(array $site, string $db, string $collection, int $sample): array
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.schema', ['db' => $db, 'collection' => $collection, 'sample' => $sample], 90);
        return ['sampled' => (int) ($o['sampled'] ?? 0), 'fields' => $o['fields'] ?? []];
    }

    /** Explain an aggregation pipeline in the account's own database. @return array{explain: mixed} */
    public function explain(array $site, string $db, string $collection, string $pipeline): array
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.explain', ['db' => $db, 'collection' => $collection, 'pipeline' => $pipeline], 90);
        return ['explain' => $o['explain'] ?? new \stdClass()];
    }

    /** @return array{indexes: mixed} */
    public function indexes(array $site, string $db, string $collection): array
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.indexes', ['db' => $db, 'collection' => $collection]);
        return ['indexes' => $o['indexes'] ?? []];
    }

    public function createIndex(array $site, string $db, string $collection, string $keys, string $options, ?int $userId): string
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.index_create', ['db' => $db, 'collection' => $collection, 'keys' => $keys, 'options' => $options], 300);
        $this->audit->record('mongo.index_create', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection], $userId);
        return (string) ($o['name'] ?? '');
    }

    public function dropIndex(array $site, string $db, string $collection, string $name, ?int $userId): void
    {
        $this->assertOwned($site, $db);
        $this->agent->call('mongo.index_drop', ['db' => $db, 'collection' => $collection, 'name' => $name]);
        $this->audit->record('mongo.index_drop', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['db' => $db, 'collection' => $collection, 'name' => $name], $userId);
    }

    /**
     * Prepend the account prefix to a customer-supplied name, removing a
     * prefix they may have typed themselves so it is never doubled.
     *
     * @param array<string, mixed> $site
     */
    private function scoped(array $site, string $name): string
    {
        $prefix = $this->prefix($site);
        $name = strtolower(trim($name));
        if ($name === '') {
            throw new UserFacingException('Enter a name.');
        }
        if (str_starts_with($name, $prefix)) {
            $name = substr($name, strlen($prefix));
        }
        return $prefix . $name;
    }

    /**
     * Refuse a name that is not under this account's prefix.
     *
     * @param array<string, mixed> $site
     */
    private function assertOwned(array $site, string $name): void
    {
        if (!str_starts_with(strtolower($name), $this->prefix($site))) {
            throw new UserFacingException('That database or user does not belong to this website.');
        }
    }
}
