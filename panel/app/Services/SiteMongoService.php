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

    /**
     * Documents in a collection of one of the account's own databases.
     *
     * @param array<string, mixed> $site
     * @return array{documents: mixed, total: int, skip: int, limit: int}
     */
    public function documents(array $site, string $db, string $collection, string $filter, int $skip, int $limit): array
    {
        $this->assertOwned($site, $db);
        $o = (array) $this->agent->call('mongo.documents', ['db' => $db, 'collection' => $collection, 'filter' => $filter, 'skip' => $skip, 'limit' => $limit], 90);
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
