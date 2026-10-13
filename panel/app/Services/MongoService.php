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

    /** @return array{databases: list<array<string, mixed>>, users: list<array<string, mixed>>} */
    public function overview(): array
    {
        $o = (array) $this->agent->call('mongo.overview');
        return [
            'databases' => array_map(static fn ($d): array => (array) $d, (array) ($o['databases'] ?? [])),
            'users' => array_map(static fn ($u): array => (array) $u, (array) ($o['users'] ?? [])),
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
}
