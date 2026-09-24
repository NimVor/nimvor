<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use DateTimeImmutable;
use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\BackupScheduler;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

final class BackupsTest extends HttpTestCase
{
    private int $siteId;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->siteId = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $this->app->container->get(UserRepository::class)->find($admin));
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run("INSERT INTO databases (website_id, name, created_at) VALUES (?, 'shop_db', '2026-01-01')", [$this->siteId]);
        $this->agent->succeed('backup.create', ['job_id' => 'cccccccccccccccc']);
        $this->agent->succeed('backup.key_info', ['exists' => true, 'key_id' => 'abcd', 'path' => '/var/lib/whacp-agent/backup.key']);
    }

    private function completeJob(): void
    {
        $this->agent->succeed('jobs.get', ['id' => 'cccccccccccccccc', 'status' => 'succeeded', 'result' => [
            'id' => '20260901T030000Z-a1b2c3', 'kind' => 'site', 'domain' => 'shop.test', 'encrypted' => true, 'local' => true, 'size' => 2048,
            'files' => [['name' => 'files.tar.gz.enc', 'kind' => 'files', 'size' => 1024, 'sha256' => 'x'], ['name' => 'db-shop_db.sql.gz.enc', 'kind' => 'database', 'database' => 'shop_db', 'size' => 1024, 'sha256' => 'y']],
        ]]);
    }

    public function testBackupRestoreDownloadDelete(): void
    {
        $base = '/sites/' . $this->siteId . '/backups';
        $this->post($base, ['files' => '1', 'databases' => '1', 'encrypt' => '1']);
        $call = $this->lastCall('backup.create');
        self::assertSame(['shop_db'], $call['databases']);
        self::assertTrue($call['encrypt']);
        self::assertSame('shop.test', $call['meta']['website']['domain']);
        self::assertSame('cccccccccccccccc', $this->flash('job'));

        $this->agent->succeed('jobs.get', ['id' => 'cccccccccccccccc', 'status' => 'running']);
        self::assertStringContainsString('running', $this->request('GET', $base)->body());
        $this->completeJob();
        $page = $this->request('GET', $base)->body();
        self::assertStringContainsString('completed', $page);
        self::assertSame('20260901T030000Z-a1b2c3', $this->db->scalar('SELECT backup_id FROM backups'));
        $id = (int) $this->db->scalar('SELECT id FROM backups');

        // Restore needs the typed domain and only offers parts in the backup.
        $this->agent->succeed('backup.restore', ['job_id' => 'dddddddddddddddd']);
        $this->post($base . '/' . $id . '/restore', ['restore_files' => '1', 'confirm_domain' => 'wrong']);
        self::assertSame(0, $this->calls('backup.restore'));
        $this->post($base . '/' . $id . '/restore', ['restore_databases' => ['other_db'], 'confirm_domain' => 'shop.test']);
        self::assertSame(0, $this->calls('backup.restore'));
        $this->post($base . '/' . $id . '/restore', ['restore_files' => '1', 'restore_databases' => ['shop_db'], 'confirm_domain' => 'shop.test']);
        $r = $this->lastCall('backup.restore');
        self::assertSame(['shop_db' => 'shop_db'], $r['databases']);
        self::assertTrue($r['safety_backup']);

        $this->agent->succeed('backup.download', ['data' => base64_encode('ENCRYPTED'), 'size' => 9, 'eof' => true]);
        $res = $this->request('GET', $base . '/' . $id . '/download', [], [], '', ['file' => 'files.tar.gz.enc']);
        self::assertSame(200, $res->status());
        self::assertStringContainsString('attachment', (string) $res->header('Content-Disposition'));
        $res = $this->request('GET', $base . '/' . $id . '/download', [], [], '', ['file' => '../../etc/shadow']);
        self::assertSame(303, $res->status());

        $this->agent->succeed('backup.delete', ['deleted' => 'x']);
        $this->post($base . '/' . $id . '/delete');
        self::assertSame('20260901T030000Z-a1b2c3', $this->lastCall('backup.delete')['id']);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM backups'));
    }

    public function testStorageSecretsAreEncryptedAndCustomersCannotUseThem(): void
    {
        $this->agent->succeed('backup.remote_test', ['ok' => true, 'objects' => 0]);
        $this->post('/backups/storages', ['name' => 'Offsite', 'endpoint' => 'https://s3.example.com', 'bucket' => 'my-backups', 'region' => 'eu-central-1', 'access_key' => 'AKIA1', 'secret_key' => 'super-secret-key-value']);
        self::assertSame('super-secret-key-value', $this->lastCall('backup.remote_test')['remote']['secret_key']);
        self::assertStringNotContainsString('super-secret-key-value', (string) json_encode($this->db->fetchAll('SELECT * FROM backup_storages')));
        self::assertStringNotContainsString('super-secret-key-value', (string) json_encode($this->db->fetchAll('SELECT * FROM audit_logs')));
        $page = $this->request('GET', '/backups')->body();
        self::assertStringContainsString('Offsite', $page);
        self::assertStringNotContainsString('super-secret', $page);
        $storage = (int) $this->db->scalar('SELECT id FROM backup_storages');

        $this->post('/sites/' . $this->siteId . '/backups', ['files' => '1', 'storage_id' => (string) $storage, 'keep_local' => '0']);
        self::assertSame('super-secret-key-value', $this->lastCall('backup.create')['remote']['secret_key']);
        self::assertFalse($this->lastCall('backup.create')['keep_local']);

        // A customer who owns a website cannot send its backups to the admin's storage.
        $cust = $this->seedUser('cust', 'customer');
        $this->db->run('UPDATE websites SET owner_id = ? WHERE id = ?', [$cust, $this->siteId]);
        $this->session = new \WhaCP\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login('cust');
        $this->post('/sites/' . $this->siteId . '/backups', ['files' => '1', 'storage_id' => (string) $storage]);
        self::assertArrayNotHasKey('remote', $this->lastCall('backup.create'));
        $this->post('/backups/storages/' . $storage . '/delete');
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM backup_storages'));
        $this->agent->succeed('backup.server', ['job_id' => 'eeeeeeeeeeeeeeee']);
        $this->post('/backups/server');
        self::assertSame(0, $this->calls('backup.server'));
    }

    public function testSchedulerDueAndRetention(): void
    {
        $s = ['frequency' => 'daily', 'hour' => 3, 'weekday' => 0, 'enabled' => 1, 'last_run_at' => null, 'created_at' => '2026-09-01T10:00:00+00:00'];
        $tz = new \DateTimeZone('UTC');
        self::assertFalse(BackupScheduler::isDue($s, new DateTimeImmutable('2026-09-02T02:59:00', $tz)), 'waits for the first slot');
        self::assertTrue(BackupScheduler::isDue($s, new DateTimeImmutable('2026-09-02T03:01:00', $tz)));
        self::assertFalse(BackupScheduler::isDue(['last_run_at' => '2026-09-02T03:01:00+00:00'] + $s, new DateTimeImmutable('2026-09-02T20:00:00', $tz)));
        $w = ['frequency' => 'weekly', 'weekday' => 1] + $s; // Mondays
        self::assertSame('2026-09-07 03:00', BackupScheduler::lastSlot($w, new DateTimeImmutable('2026-09-10T12:00:00', $tz))->format('Y-m-d H:i'));

        $this->post('/sites/' . $this->siteId . '/backup-schedules', ['frequency' => 'daily', 'hour' => '3', 'retention' => '2', 'include_files' => '1', 'include_databases' => '0']);
        $sched = (int) $this->db->scalar('SELECT id FROM backup_schedules');
        self::assertGreaterThan(0, $sched);
        $this->db->run("UPDATE backup_schedules SET created_at = '2026-01-01T00:00:00+00:00'");
        $this->completeJob();
        $scheduler = $this->app->container->get(BackupScheduler::class);
        $scheduler->run(new DateTimeImmutable('2026-09-02T03:05:00', $tz));
        self::assertSame([], $this->lastCall('backup.create')['databases']);
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM backups WHERE schedule_id = ?', [$sched]));
        $scheduler->run(new DateTimeImmutable('2026-09-02T03:10:00', $tz));
        self::assertSame(1, $this->calls('backup.create'), 'runs once per slot');

        // Retention keeps the newest two completed backups.
        foreach (['a', 'b', 'c'] as $i => $x) {
            $this->db->run("INSERT INTO backups (website_id, schedule_id, kind, domain, backup_id, status, created_at) VALUES (?, ?, 'site', 'shop.test', ?, 'completed', ?)", [$this->siteId, $sched, "2026090{$i}T030000Z-00000{$x}", '2026-09-0' . ($i + 3)]);
        }
        $this->agent->succeed('backup.delete', ['deleted' => 'x']);
        $scheduler->prune();
        self::assertSame(2, (int) $this->db->scalar("SELECT COUNT(*) FROM backups WHERE schedule_id = ? AND status = 'completed'", [$sched]));
        self::assertSame(2, $this->calls('backup.delete'));
    }
}
