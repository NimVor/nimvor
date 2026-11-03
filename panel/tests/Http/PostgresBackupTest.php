<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Services\PostgresService;
use NimVor\Tests\Support\HttpTestCase;

/** PostgreSQL backups and monitoring: ownership, what is sent to the agent, schedules and who may see the health page. */
final class PostgresBackupTest extends HttpTestCase
{
    private Connection $db;
    private int $aliceDb;
    private const FILE = '20260901-120000_alice_shop.dump';

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('viewer', 'viewer');
        $this->seedUser('alice', 'customer');
        $this->seedUser('bob', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('pg.status', ['installed' => true, 'running' => true, 'managed' => true, 'version' => '16.4', 'port' => 5432]);
        $this->agent->succeed('pg.overview', ['databases' => [], 'roles' => [], 'version' => '16.4', 'max_connections' => 100, 'active_connections' => 1]);
        foreach (['pg.role_create', 'pg.database_create', 'pg.backup_delete', 'pg.backup_prune'] as $a) {
            $this->agent->succeed($a, ['ok' => true]);
        }
        $this->agent->succeed('pg.backup_create', ['job_id' => 'job-1']);
        $this->agent->succeed('pg.backup_restore', ['job_id' => 'job-2']);
        $this->agent->succeed('pg.backup_list', ['backups' => [['file' => self::FILE, 'database' => 'alice_shop', 'size' => 2048, 'created' => '2026-09-01T12:00:00Z', 'sha256' => str_repeat('ab', 32)]]]);
        $this->agent->succeed('pg.monitor', [
            'version' => 'PostgreSQL 16.4 on x86_64', 'uptime_seconds' => 90061, 'max_connections' => 100,
            'connections' => ['total' => 7, 'by_state' => ['active' => 2]], 'lock_waits' => 1,
            'databases' => [['name' => 'alice_shop', 'size_bytes' => 8192, 'connections' => 1, 'commits' => 10, 'rollbacks' => 0, 'cache_hit_percent' => 99.5, 'deadlocks' => 0, 'temp_bytes' => 0]],
            'long_running' => [['pid' => 5, 'database' => 'alice_shop', 'user' => 'alice_app', 'state' => 'active', 'seconds' => 61, 'statement' => 'SELECT']],
        ]);
        $this->login('alice');
        $this->post('/postgres/databases', ['name' => 'shop']);
        $this->aliceDb = (int) $this->db->scalar("SELECT id FROM pg_databases WHERE name = 'alice_shop'");
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    public function testOwnerSeesTheBackupsOfTheirDatabase(): void
    {
        $page = $this->request('GET', '/postgres/backups', [], [], '', ['db' => (string) $this->aliceDb])->body();
        self::assertStringContainsString(self::FILE, $page);
        self::assertStringContainsString('Back up now', $page);
        self::assertSame('alice_shop', $this->lastCall('pg.backup_list')['database']);
    }

    public function testAnotherCustomerCannotUseThem(): void
    {
        $this->switchTo('bob');
        $before = $this->calls('pg.backup_list');
        $this->request('GET', '/postgres/backups', [], [], '', ['db' => (string) $this->aliceDb]);
        self::assertSame($before, $this->calls('pg.backup_list'));
        foreach (['/postgres/backups', '/postgres/backups/restore', '/postgres/backups/delete'] as $path) {
            $this->post($path, ['db' => (string) $this->aliceDb, 'file' => self::FILE]);
        }
        self::assertSame(0, $this->calls('pg.backup_create') + $this->calls('pg.backup_restore') + $this->calls('pg.backup_delete'));
    }

    public function testBackupAndRestoreStartJobsAndAreAudited(): void
    {
        $this->post('/postgres/backups', ['db' => (string) $this->aliceDb]);
        self::assertSame('alice_shop', $this->lastCall('pg.backup_create')['database']);
        $this->post('/postgres/backups/restore', ['db' => (string) $this->aliceDb, 'file' => self::FILE]);
        $call = $this->lastCall('pg.backup_restore');
        self::assertSame(['alice_shop', self::FILE], [$call['database'], $call['file']]);
        $audit = (string) json_encode($this->db->fetchAll('SELECT * FROM audit_logs'));
        self::assertStringContainsString('postgres.backup', $audit);
        self::assertStringContainsString('postgres.restore', $audit);
    }

    public function testOnlyOwnFilesAreAccepted(): void
    {
        foreach (['20260901-120000_bob_other.dump', '../../etc/passwd', self::FILE . '.sha256', ''] as $bad) {
            $this->post('/postgres/backups/restore', ['db' => (string) $this->aliceDb, 'file' => $bad]);
            $this->post('/postgres/backups/delete', ['db' => (string) $this->aliceDb, 'file' => $bad]);
        }
        self::assertSame(0, $this->calls('pg.backup_restore') + $this->calls('pg.backup_delete'));
        $this->post('/postgres/backups/delete', ['db' => (string) $this->aliceDb, 'file' => self::FILE]);
        self::assertSame(1, $this->calls('pg.backup_delete'));
    }

    public function testViewerCannotChangeBackups(): void
    {
        $this->switchTo('viewer');
        $this->post('/postgres/backups', ['db' => (string) $this->aliceDb]);
        $this->post('/postgres/backups/schedule', ['db' => (string) $this->aliceDb, 'frequency' => 'daily', 'hour' => '3', 'keep' => '7']);
        self::assertSame(0, $this->calls('pg.backup_create'));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM pg_backup_schedules'));
    }

    public function testScheduleIsValidatedSavedAndRemoved(): void
    {
        $body = ['db' => (string) $this->aliceDb];
        $this->post('/postgres/backups/schedule', $body + ['frequency' => 'hourly', 'hour' => '3', 'keep' => '7']);
        $this->post('/postgres/backups/schedule', $body + ['frequency' => 'daily', 'hour' => '25', 'keep' => '7']);
        $this->post('/postgres/backups/schedule', $body + ['frequency' => 'daily', 'hour' => '3', 'keep' => '0']);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM pg_backup_schedules'));
        $this->post('/postgres/backups/schedule', $body + ['frequency' => 'weekly', 'hour' => '4', 'keep' => '5']);
        $this->post('/postgres/backups/schedule', $body + ['frequency' => 'daily', 'hour' => '2', 'keep' => '9']);
        $row = $this->db->fetchAll('SELECT * FROM pg_backup_schedules');
        self::assertCount(1, $row);
        self::assertSame(['daily', 2, 9], [$row[0]['frequency'], (int) $row[0]['hour'], (int) $row[0]['keep']]);
        $this->post('/postgres/backups/schedule/delete', $body);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM pg_backup_schedules'));
    }

    public function testDueBackupsRunOncePerSlotAndPruneFirst(): void
    {
        $this->post('/postgres/backups/schedule', ['db' => (string) $this->aliceDb, 'frequency' => 'daily', 'hour' => '3', 'keep' => '4']);
        $svc = $this->app->container->get(PostgresService::class);
        $now = (int) gmmktime(5, 0, 0, 9, 2, 2026);
        $lines = $svc->runDueBackups($now);
        self::assertCount(1, $lines);
        self::assertSame(4, $this->lastCall('pg.backup_prune')['keep']);
        self::assertSame('alice_shop', $this->lastCall('pg.backup_create')['database']);
        $created = $this->calls('pg.backup_create');
        self::assertSame([], $svc->runDueBackups($now + 600), 'the same slot runs only once');
        self::assertSame($created, $this->calls('pg.backup_create'));
        self::assertCount(1, $svc->runDueBackups($now + 86400), 'the next day runs again');
    }

    public function testSlots(): void
    {
        $now = (int) gmmktime(5, 0, 0, 9, 2, 2026); // Wednesday
        self::assertSame((int) gmmktime(3, 0, 0, 9, 2, 2026), PostgresService::lastSlot('daily', 3, $now));
        self::assertSame((int) gmmktime(6, 0, 0, 9, 1, 2026), PostgresService::lastSlot('daily', 6, $now));
        self::assertSame((int) gmmktime(3, 0, 0, 8, 30, 2026), PostgresService::lastSlot('weekly', 3, $now));
    }

    public function testHealthPageIsForServerAdministrators(): void
    {
        $this->switchTo('admin');
        $page = $this->request('GET', '/postgres/monitor')->body();
        self::assertStringContainsString('7 of 100', $page);
        self::assertStringContainsString('alice_shop', $page);
        self::assertStringContainsString('SELECT', $page);
        self::assertStringContainsString('1 d', $page);
        $this->switchTo('alice');
        $before = $this->calls('pg.monitor');
        $res = $this->request('GET', '/postgres/monitor');
        self::assertSame($before, $this->calls('pg.monitor'));
        self::assertNotSame(200, $res->status());
    }
}
