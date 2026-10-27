<?php

declare(strict_types=1);

namespace NimVor\Tests\Support;

use PHPUnit\Framework\TestCase;
use NimVor\Database\Connection;
use NimVor\Database\Migrator;
use NimVor\Repositories\AuditLogRepository;
use NimVor\Repositories\RoleRepository;
use NimVor\Repositories\UserRepository;
use NimVor\Security\PasswordHasher;
use NimVor\Services\AuditLogger;
use NimVor\Support\Logger;

/**
 * Provides a migrated in-memory SQLite database.
 */
abstract class DatabaseTestCase extends TestCase
{
    protected Connection $db;
    protected RoleRepository $roles;
    protected UserRepository $users;
    protected AuditLogger $audit;
    protected PasswordHasher $hasher;

    protected function setUp(): void
    {
        $this->db = Connection::sqlite(':memory:');
        (new Migrator($this->db, dirname(__DIR__, 2) . '/database/migrations'))->migrate();
        $this->roles = new RoleRepository($this->db);
        $this->roles->syncDefaults();
        $this->users = new UserRepository($this->db);
        $this->audit = new AuditLogger(new AuditLogRepository($this->db), new Logger('/dev/null'));
        $this->hasher = new PasswordHasher();
    }

    protected function createUser(string $username, string $role = 'admin', string $password = 'a-long-test-password', bool $active = true): int
    {
        return $this->users->create([
            'username' => $username,
            'email' => $username . '@example.test',
            'display_name' => '',
            'password_hash' => $this->hasher->hash($password),
            'role_id' => (int) $this->roles->findByName($role)['id'],
            'is_active' => $active,
        ]);
    }

    protected function roleId(string $name): int
    {
        return (int) $this->roles->findByName($name)['id'];
    }

    /** @return list<array<string, mixed>> */
    protected function auditRows(): array
    {
        return $this->db->fetchAll('SELECT * FROM audit_logs ORDER BY id');
    }
}
