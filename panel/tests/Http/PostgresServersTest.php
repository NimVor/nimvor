<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Services\PostgresService;
use NimVor\Tests\Support\HttpTestCase;

/** Registered (remote) PostgreSQL servers: who may add them, where they may point, and that the credentials are used only for the agent. */
final class PostgresServersTest extends HttpTestCase
{
    private Connection $db;
    private const PASSWORD = 'Rem0te-secret-pass-123';

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('alice', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('pg.status', ['installed' => true, 'running' => true, 'managed' => true, 'version' => '16.4', 'port' => 5432]);
        $this->agent->succeed('pg.overview', ['databases' => [], 'roles' => [], 'version' => '16.4', 'max_connections' => 100, 'active_connections' => 1]);
        $this->agent->succeed('pg.test', ['ok' => true, 'version' => '15.6', 'superuser' => false]);
        foreach (['pg.role_create', 'pg.database_create', 'pg.tables', 'pg.backup_create', 'pg.backup_prune', 'pg.role_delete'] as $a) {
            $this->agent->succeed($a, ['ok' => true, 'job_id' => 'j1', 'schemas' => [], 'tables' => []]);
        }
        $this->login('admin');
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    /** @param array<string, string> $over */
    private function register(array $over = []): \NimVor\Http\Response
    {
        return $this->post('/postgres/servers', $over + ['name' => 'Managed DB', 'host' => '203.0.113.10', 'port' => '5432', 'username' => 'nimvor_admin', 'password' => self::PASSWORD, 'ssl_mode' => 'verify-full', 'ca_pem' => '']);
    }

    private function remoteId(): int
    {
        return (int) $this->db->scalar("SELECT id FROM pg_servers WHERE connection_type = 'remote' ORDER BY id LIMIT 1");
    }

    public function testAdminRegistersAServerAfterATestAndTheSecretIsEncrypted(): void
    {
        $this->register();
        $row = $this->db->fetchAll("SELECT * FROM pg_servers WHERE connection_type = 'remote'");
        self::assertCount(1, $row);
        self::assertSame(['203.0.113.10', 5432, 'nimvor_admin', 'verify-full', '15.6'], [$row[0]['host'], (int) $row[0]['port'], $row[0]['username'], $row[0]['ssl_mode'], $row[0]['version']]);
        self::assertStringNotContainsString(self::PASSWORD, (string) $row[0]['credential_enc']);
        self::assertNotSame('', $row[0]['credential_enc']);
        $t = $this->lastCall('pg.test')['target'];
        self::assertSame(['203.0.113.10', 5432, 'nimvor_admin', self::PASSWORD, 'verify-full'], [$t['host'], $t['port'], $t['user'], $t['password'], $t['ssl_mode']]);
        $audit = (string) json_encode($this->db->fetchAll('SELECT * FROM audit_logs'));
        self::assertStringContainsString('postgres.server_register', $audit);
        self::assertStringNotContainsString(self::PASSWORD, $audit);
        $page = $this->request('GET', '/postgres')->body();
        self::assertStringContainsString('Managed DB', $page);
        self::assertStringNotContainsString(self::PASSWORD, $page);
    }

    public function testUnencryptedModesAreRefused(): void
    {
        foreach (['disable', 'prefer', 'allow', ''] as $mode) {
            $this->register(['ssl_mode' => $mode, 'name' => 'S ' . $mode]);
        }
        self::assertSame(0, $this->remoteId());
        self::assertSame(0, $this->calls('pg.test'));
    }

    public function testVerifyCaNeedsACertificate(): void
    {
        $this->register(['ssl_mode' => 'verify-ca']);
        self::assertSame(0, $this->remoteId());
        $this->register(['ssl_mode' => 'verify-ca', 'ca_pem' => "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"]);
        self::assertGreaterThan(0, $this->remoteId());
    }

    public function testThisMachineAndLinkLocalAddressesAreRefused(): void
    {
        foreach (['127.0.0.1', '127.1.2.3', '::1', '0.0.0.0', '169.254.169.254', 'fe80::1', '::ffff:127.0.0.1', '224.0.0.1', 'bad host!', ''] as $host) {
            self::assertNotNull(PostgresService::destinationProblem($host), $host);
        }
        foreach (['203.0.113.10', '10.0.0.5', '192.168.1.20', '2001:db8::1'] as $host) {
            self::assertNull(PostgresService::destinationProblem($host), $host);
        }
        $this->register(['host' => '169.254.169.254']);
        $this->register(['host' => '127.0.0.1', 'name' => 'Loop']);
        self::assertSame(0, $this->remoteId());
        self::assertSame(0, $this->calls('pg.test'));
    }

    public function testAFailedConnectionIsNotRemembered(): void
    {
        $this->agent->fail('pg.test', 502, 'failed', 'The password was refused.');
        $this->register();
        self::assertSame(0, $this->remoteId());
    }

    public function testCustomersCannotRegisterTestOrRemoveServers(): void
    {
        $this->register();
        $id = $this->remoteId();
        $this->switchTo('alice');
        $tests = $this->calls('pg.test');
        $this->post('/postgres/servers', ['name' => 'Mine', 'host' => '203.0.113.11', 'port' => '5432', 'username' => 'u', 'password' => 'p', 'ssl_mode' => 'require']);
        $this->post('/postgres/servers/test', ['id' => (string) $id]);
        $this->post('/postgres/servers/remove', ['id' => (string) $id]);
        self::assertSame($tests, $this->calls('pg.test'));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM pg_servers WHERE connection_type = 'remote'"));
    }

    public function testAdministratorCreatesADatabaseOnTheRegisteredServer(): void
    {
        $this->register();
        $id = $this->remoteId();
        $this->post('/postgres/databases', ['name' => 'warehouse', 'pg_server_id' => (string) $id]);
        $call = $this->lastCall('pg.database_create');
        self::assertSame('warehouse', $call['name']);
        self::assertSame(['203.0.113.10', self::PASSWORD], [$call['target']['host'], $call['target']['password']]);
        self::assertSame($id, (int) $this->db->scalar("SELECT pg_server_id FROM pg_databases WHERE name = 'warehouse'"));
        self::assertSame($id, (int) $this->db->scalar("SELECT pg_server_id FROM pg_roles WHERE name = 'warehouse'"));
        // The local server never gets a target.
        $this->post('/postgres/databases', ['name' => 'localone']);
        self::assertArrayNotHasKey('target', $this->lastCall('pg.database_create'));
        // Using the database later goes to the same server.
        $dbId = (int) $this->db->scalar("SELECT id FROM pg_databases WHERE name = 'warehouse'");
        $this->request('GET', '/postgres/browse', [], [], '', ['db' => (string) $dbId]);
        self::assertSame('203.0.113.10', $this->lastCall('pg.tables')['target']['host']);
    }

    public function testCustomersAlwaysUseTheLocalServer(): void
    {
        $this->register();
        $id = $this->remoteId();
        $this->switchTo('alice');
        $before = $this->calls('pg.database_create');
        $this->post('/postgres/databases', ['name' => 'shop', 'pg_server_id' => (string) $id]);
        self::assertSame($before, $this->calls('pg.database_create'), 'a customer may not choose a registered server');
        $this->post('/postgres/databases', ['name' => 'shop']);
        self::assertArrayNotHasKey('target', $this->lastCall('pg.database_create'));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM pg_databases WHERE pg_server_id = ?', [$id]));
    }

    public function testAServerWithDatabasesCannotBeRemoved(): void
    {
        $this->register();
        $id = $this->remoteId();
        $this->post('/postgres/databases', ['name' => 'warehouse', 'pg_server_id' => (string) $id]);
        $this->post('/postgres/servers/remove', ['id' => (string) $id]);
        self::assertSame($id, $this->remoteId());
        $this->db->run('DELETE FROM pg_databases WHERE pg_server_id = ?', [$id]);
        $this->db->run('DELETE FROM pg_roles WHERE pg_server_id = ?', [$id]);
        $this->post('/postgres/servers/remove', ['id' => (string) $id]);
        self::assertSame(0, $this->remoteId());
    }

    public function testTheLocalServerCannotBeTestedOrRemoved(): void
    {
        $local = (int) $this->db->scalar("SELECT id FROM pg_servers WHERE connection_type = 'local'");
        $this->request('GET', '/postgres');
        $local = (int) $this->db->scalar("SELECT id FROM pg_servers WHERE connection_type = 'local'");
        $this->post('/postgres/servers/remove', ['id' => (string) $local]);
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM pg_servers WHERE connection_type = 'local'"));
    }

    public function testScheduledBackupsRunOnTheRegisteredServer(): void
    {
        $this->register();
        $id = $this->remoteId();
        $this->post('/postgres/databases', ['name' => 'warehouse', 'pg_server_id' => (string) $id]);
        $dbId = (int) $this->db->scalar("SELECT id FROM pg_databases WHERE name = 'warehouse'");
        $this->post('/postgres/backups/schedule', ['db' => (string) $dbId, 'frequency' => 'daily', 'hour' => '3', 'keep' => '3']);
        $lines = $this->app->container->get(PostgresService::class)->runDueBackups((int) gmmktime(5, 0, 0, 9, 2, 2026));
        self::assertCount(1, $lines);
        self::assertSame('203.0.113.10', $this->lastCall('pg.backup_create')['target']['host']);
        self::assertSame('203.0.113.10', $this->lastCall('pg.backup_prune')['target']['host']);
    }

    public function testHealthPageCanShowARegisteredServer(): void
    {
        $this->agent->succeed('pg.monitor', ['version' => 'PostgreSQL 15.6', 'uptime_seconds' => 10, 'max_connections' => 50, 'connections' => ['total' => 1, 'by_state' => []], 'lock_waits' => 0, 'databases' => [], 'long_running' => []]);
        $this->register();
        $id = $this->remoteId();
        $page = $this->request('GET', '/postgres/monitor', [], [], '', ['server' => (string) $id])->body();
        self::assertStringContainsString('1 of 50', $page);
        self::assertSame('203.0.113.10', $this->lastCall('pg.monitor')['target']['host']);
        $this->request('GET', '/postgres/monitor', [], [], '', ['server' => '99999']);
        self::assertArrayNotHasKey('target', $this->lastCall('pg.monitor'), 'an unknown server falls back to the local one');
    }
}
