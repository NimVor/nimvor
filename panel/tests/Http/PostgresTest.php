<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Tests\Support\HttpTestCase;

/** PostgreSQL: who can see and change what, naming, limits and the calls made to the agent. */
final class PostgresTest extends HttpTestCase
{
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('alice', 'customer');
        $this->seedUser('bob', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('pg.status', ['installed' => true, 'running' => true, 'managed' => true, 'version' => '16.4', 'port' => 5432]);
        $this->agent->succeed('pg.overview', ['databases' => [
            ['name' => 'postgres', 'owner' => 'postgres', 'size_bytes' => 8000000, 'connections' => 0, 'system' => true],
            ['name' => 'legacy_app', 'owner' => 'legacy', 'size_bytes' => 4096, 'connections' => 1, 'system' => false],
        ], 'roles' => [], 'version' => '16.4', 'max_connections' => 100, 'active_connections' => 3]);
        foreach (['pg.role_create', 'pg.role_password', 'pg.role_delete', 'pg.database_create', 'pg.database_delete', 'pg.grant_set', 'pg.install', 'pg.adopt', 'pg.service'] as $a) {
            $this->agent->succeed($a, ['ok' => true, 'job_id' => 'abcdef0123456789']);
        }
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    public function testCustomerCreatesPrefixedDatabaseWithItsOwnerUser(): void
    {
        $this->login('alice');
        $res = $this->post('/postgres/databases', ['name' => 'shop']);
        self::assertSame('/postgres', $res->header('Location'));
        self::assertSame('alice_shop', $this->lastCall('pg.database_create')['name']);
        self::assertSame('alice_shop', $this->lastCall('pg.database_create')['owner']);
        $role = $this->lastCall('pg.role_create');
        self::assertSame('alice_shop', $role['name']);
        self::assertGreaterThanOrEqual(20, strlen((string) $role['password']));
        $secret = (array) $this->flash('secret');
        self::assertSame($role['password'], $secret['password'], 'the generated password is shown once');
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM pg_databases WHERE name = 'alice_shop' AND owner_id = (SELECT id FROM users WHERE username = 'alice')"));
        // Never stored unless asked.
        self::assertSame('', (string) $this->db->scalar("SELECT password_enc FROM pg_roles WHERE name = 'alice_shop'"));
        self::assertStringContainsString('alice_shop', $this->request('GET', '/postgres')->body());
    }

    public function testBadNamesNeverReachTheAgent(): void
    {
        $this->login('alice');
        foreach (['sh op', 'shop;drop', 'a"b', 'sh-op', 'ünï', str_repeat('x', 60), '', '   '] as $bad) {
            $this->post('/postgres/databases', ['name' => $bad]);
        }
        self::assertSame(0, $this->calls('pg.database_create'));
        self::assertSame(0, $this->calls('pg.role_create'));
        $this->login('admin');
    }

    public function testReservedNamesAreRefused(): void
    {
        $this->login('admin');
        foreach (['postgres', 'template1', 'pg_hack', 'nimvor_admin', 'public'] as $name) {
            $this->post('/postgres/databases', ['name' => $name]);
            $this->post('/postgres/users', ['username' => $name]);
        }
        self::assertSame(0, $this->calls('pg.database_create'));
        self::assertSame(0, $this->calls('pg.role_create'));
    }

    public function testCustomersNeverSeeOrChangeEachOthersData(): void
    {
        $this->login('alice');
        $this->post('/postgres/databases', ['name' => 'shop']);
        $this->request('GET', '/postgres'); // shows (and clears) the one-time password
        $aliceDb = (int) $this->db->scalar("SELECT id FROM pg_databases WHERE name = 'alice_shop'");
        $aliceRole = (int) $this->db->scalar("SELECT id FROM pg_roles WHERE name = 'alice_shop'");

        $this->switchTo('bob');
        $this->post('/postgres/databases', ['name' => 'blog']);
        $this->request('GET', '/postgres');
        $bobRole = (int) $this->db->scalar("SELECT id FROM pg_roles WHERE name = 'bob_blog'");
        $page = $this->request('GET', '/postgres')->body();
        self::assertStringContainsString('bob_blog', $page);
        self::assertStringNotContainsString('alice_shop', $page, "alice's database is listed for bob");

        $before = $this->calls('pg.database_delete') + $this->calls('pg.role_delete') + $this->calls('pg.role_password') + $this->calls('pg.grant_set');
        $this->post('/postgres/databases/drop', ['id' => (string) $aliceDb, 'confirm' => 'alice_shop']);
        $this->post('/postgres/users/drop', ['id' => (string) $aliceRole]);
        $this->post('/postgres/users/password', ['id' => (string) $aliceRole]);
        $this->post('/postgres/grants', ['database_id' => (string) $aliceDb, 'role_id' => (string) $bobRole, 'level' => 'readwrite']);
        $this->post('/postgres/grants', ['database_id' => (string) $aliceDb, 'role_id' => (string) $aliceRole, 'level' => 'all']);
        self::assertSame($before, $this->calls('pg.database_delete') + $this->calls('pg.role_delete') + $this->calls('pg.role_password') + $this->calls('pg.grant_set'), "bob's requests reached the agent");
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM pg_databases WHERE name = 'alice_shop'"));

        // Bob cannot use alice's user as the owner of his database either.
        $this->post('/postgres/databases', ['name' => 'steal', 'owner_role_id' => (string) $aliceRole]);
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM pg_databases WHERE name = 'bob_steal'"));
    }

    public function testAccessMustStayInsideOneAccount(): void
    {
        // An administrator who can see both accounts still cannot give one customer's user another's database.
        $this->login('alice');
        $this->post('/postgres/databases', ['name' => 'shop']);
        $this->switchTo('bob');
        $this->post('/postgres/databases', ['name' => 'blog']);
        $this->switchTo('admin');
        $aliceDb = (int) $this->db->scalar("SELECT id FROM pg_databases WHERE name = 'alice_shop'");
        $bobRole = (int) $this->db->scalar("SELECT id FROM pg_roles WHERE name = 'bob_blog'");
        $this->post('/postgres/grants', ['database_id' => (string) $aliceDb, 'role_id' => (string) $bobRole, 'level' => 'readonly']);
        self::assertSame(0, $this->calls('pg.grant_set'));
    }

    public function testGrantPasswordAndDeleteFlowForTheOwner(): void
    {
        $this->login('alice');
        $this->post('/postgres/databases', ['name' => 'Shop']); // upper case is lowered
        $this->post('/postgres/users', ['username' => 'reader']);
        $dbId = (int) $this->db->scalar("SELECT id FROM pg_databases WHERE name = 'alice_shop'");
        $reader = (int) $this->db->scalar("SELECT id FROM pg_roles WHERE name = 'alice_reader'");
        self::assertGreaterThan(0, $reader);

        $this->post('/postgres/grants', ['database_id' => (string) $dbId, 'role_id' => (string) $reader, 'level' => 'readonly']);
        self::assertSame(['database' => 'alice_shop', 'role' => 'alice_reader', 'level' => 'readonly'], $this->lastCall('pg.grant_set'));
        self::assertSame('readonly', (string) $this->db->scalar('SELECT level FROM pg_grants WHERE database_id = ? AND role_id = ?', [$dbId, $reader]));
        $this->post('/postgres/grants', ['database_id' => (string) $dbId, 'role_id' => (string) $reader, 'level' => 'none']);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM pg_grants'));
        $this->post('/postgres/grants', ['database_id' => (string) $dbId, 'role_id' => (string) $reader, 'level' => 'superuser']);
        self::assertSame('none', $this->lastCall('pg.grant_set')['level'], 'an unknown level never reaches the agent');

        $this->post('/postgres/users/password', ['id' => (string) $reader]);
        self::assertSame('alice_reader', $this->lastCall('pg.role_password')['name']);

        // The owner cannot be dropped while it owns the database; the database needs its name typed.
        $owner = (int) $this->db->scalar("SELECT id FROM pg_roles WHERE name = 'alice_shop'");
        $this->post('/postgres/users/drop', ['id' => (string) $owner]);
        self::assertSame(0, $this->calls('pg.role_delete'), 'an owner is not dropped while it owns a database');
        $this->post('/postgres/databases/drop', ['id' => (string) $dbId, 'confirm' => 'wrong']);
        self::assertSame(0, $this->calls('pg.database_delete'));
        $this->post('/postgres/databases/drop', ['id' => (string) $dbId, 'confirm' => 'alice_shop']);
        self::assertSame('alice_shop', $this->lastCall('pg.database_delete')['name']);
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM pg_databases WHERE name = 'alice_shop'"));
        $this->post('/postgres/users/drop', ['id' => (string) $owner]);
        self::assertSame('alice_shop', $this->lastCall('pg.role_delete')['name']);
    }

    public function testPlanLimitCountsPostgresDatabases(): void
    {
        $alice = (int) $this->db->scalar("SELECT id FROM users WHERE username = 'alice'");
        $this->db->run("INSERT INTO plans (name, max_databases, created_at, updated_at) VALUES ('tiny', 1, 'x', 'x')");
        $planId = (int) $this->db->scalar("SELECT id FROM plans WHERE name = 'tiny'");
        $this->db->run('UPDATE users SET plan_id = ? WHERE id = ?', [$planId, $alice]);
        $this->login('alice');
        $this->post('/postgres/databases', ['name' => 'one']);
        self::assertSame(1, $this->calls('pg.database_create'));
        $this->post('/postgres/databases', ['name' => 'two']);
        self::assertSame(1, $this->calls('pg.database_create'), 'the plan allows one SQL database');
    }

    public function testServerControlsNeedThePostgresPermission(): void
    {
        $this->login('alice');
        foreach (['/postgres/install', '/postgres/adopt', '/postgres/service'] as $path) {
            self::assertSame(403, $this->post($path, ['op' => 'restart'])->status(), $path);
        }
        self::assertSame(0, $this->calls('pg.install') + $this->calls('pg.adopt') + $this->calls('pg.service'));
        $this->switchTo('admin');
        $this->post('/postgres/service', ['op' => 'restart']);
        self::assertSame('restart', $this->lastCall('pg.service')['verb']);
        $this->post('/postgres/service', ['op' => 'rm -rf']);
        self::assertSame('restart', $this->lastCall('pg.service')['verb']);
        $this->post('/postgres/adopt', ['harden' => '1']);
        self::assertSame(['confirm' => true, 'harden' => true], $this->lastCall('pg.adopt'));
    }

    public function testAdministratorSeesServerAndUnmanagedDatabases(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/postgres')->body();
        self::assertStringContainsString('PostgreSQL 16.4', $page);
        self::assertStringContainsString('3 of 100 connections', $page);
        self::assertStringContainsString('legacy_app', $page, 'a database without a record is listed (read-only)');
        self::assertStringNotContainsString('template1', $page);
        $this->login('alice');
    }

    public function testNotInstalledAndNotManagedStates(): void
    {
        $this->agent->succeed('pg.status', ['installed' => false, 'running' => false, 'managed' => false, 'version' => '', 'port' => 5432]);
        $this->login('admin');
        self::assertStringContainsString('Install PostgreSQL', $this->request('GET', '/postgres')->body());
        $this->post('/postgres/install');
        self::assertSame(1, $this->calls('pg.install'));
        $this->switchTo('alice');
        $body = $this->request('GET', '/postgres')->body();
        self::assertStringNotContainsString('Install PostgreSQL', $body);
        self::assertStringContainsString('Ask your hosting provider', $body);
        $this->agent->succeed('pg.status', ['installed' => true, 'running' => true, 'managed' => false, 'version' => '15.2', 'port' => 5432]);
        $this->switchTo('admin');
        self::assertStringContainsString('does not manage this PostgreSQL server yet', $this->request('GET', '/postgres')->body());
    }

    public function testNoPasswordAppearsInPagesOrAuditTrail(): void
    {
        $this->login('alice');
        $this->post('/postgres/databases', ['name' => 'shop']);
        $pw = (string) $this->lastCall('pg.role_create')['password'];
        $this->request('GET', '/postgres');
        self::assertStringNotContainsString($pw, $this->request('GET', '/postgres')->body());
        self::assertStringNotContainsString($pw, (string) json_encode($this->db->fetchAll('SELECT * FROM audit_logs')));
    }
}
