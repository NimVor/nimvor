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

    /**
     * Backup storage is server-wide: customers and resellers hold
     * backups.manage for their own websites but cannot add a destination,
     * so they cannot make the agent connect to an address of their choice
     * (internal hosts, metadata services).
     */
    public function testCustomersAndResellersCannotAddStorage(): void
    {
        foreach (['customer', 'reseller'] as $role) {
            $this->seedUser('u' . $role, $role);
            $this->session = new \WhaCP\Http\Session\ArraySession();
            $this->app = $this->makeApp();
            $this->login('u' . $role);
            $this->post('/backups/storages', ['name' => 'Internal', 'type' => 'sftp', 'host' => '10.0.0.5', 'username' => 'u1', 'password' => 'pw-internal-1']);
            $this->post('/backups/storages', ['name' => 'Meta', 'endpoint' => 'https://169.254.169.254', 'bucket' => 'latest', 'region' => 'us-east-1', 'access_key' => 'a', 'secret_key' => 'b']);
            $this->post('/backups/storages/confirm', ['trust' => '1']);
            $this->request('GET', '/backups/storages/google/callback?state=x&code=y');
            self::assertSame(0, $this->calls('backup.remote_test'), $role);
            self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM backup_storages'), $role);
        }
    }

    public function testSftpStorageAsksToTrustTheHostKey(): void
    {
        $key = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl';
        $this->agent->succeed('backup.remote_test', ['ok' => true, 'host_key' => $key, 'fingerprint' => 'SHA256:abcdefFINGERPRINT']);
        $res = $this->post('/backups/storages', ['name' => 'Box', 'type' => 'sftp', 'host' => 'backup.example.com', 'username' => 'u1', 'password' => 'sftp-secret-pass', 'prefix' => 'web1']);
        self::assertSame('/backups/storages/confirm', $res->header('Location'));
        $remote = $this->lastCall('backup.remote_test')['remote'];
        self::assertSame(['sftp', 'backup.example.com', 'u1', 'sftp-secret-pass', 'web1'], [$remote['type'], $remote['host'], $remote['username'], $remote['password'], $remote['prefix']]);
        self::assertArrayNotHasKey('host_key', $remote);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM backup_storages'));
        // The pending credentials are sealed in the session.
        self::assertStringNotContainsString('sftp-secret-pass', (string) json_encode($this->session->get('backup_storage_pending')));

        $page = $this->request('GET', '/backups/storages/confirm')->body();
        self::assertStringContainsString('SHA256:abcdefFINGERPRINT', $page);
        self::assertStringContainsString('ssh_host_ed25519_key.pub', $page);

        $this->post('/backups/storages/confirm', ['trust' => '1']);
        self::assertSame($key, $this->lastCall('backup.remote_test')['remote']['host_key'], 'tested again with the key pinned');
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM backup_storages WHERE kind = 'sftp'"));
        self::assertStringContainsString('sftp://u1@backup.example.com', $this->request('GET', '/backups')->body());
        $this->post('/sites/' . $this->siteId . '/backups', ['files' => '1', 'storage_id' => (string) $this->db->scalar('SELECT id FROM backup_storages')]);
        self::assertSame($key, $this->lastCall('backup.create')['remote']['host_key']);
        // The confirmation cannot be replayed.
        $this->post('/backups/storages/confirm', ['trust' => '1']);
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM backup_storages'));
    }

    public function testCancelledTrustSavesNothing(): void
    {
        $this->agent->succeed('backup.remote_test', ['ok' => true, 'tls_pin' => 'sha256//' . str_repeat('A', 43) . '=', 'tls_subject' => 'CN=ftp.example.com']);
        $this->post('/backups/storages', ['name' => 'Ftp', 'type' => 'ftp', 'host' => 'ftp.example.com', 'username' => 'u1', 'password' => 'pw']);
        self::assertSame('explicit', $this->lastCall('backup.remote_test')['remote']['tls']);
        self::assertStringContainsString('CN=ftp.example.com', $this->request('GET', '/backups/storages/confirm')->body());
        $this->post('/backups/storages/confirm', ['cancel' => '1']);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM backup_storages'));
        self::assertNull($this->session->get('backup_storage_pending'));
    }

    public function testStorageValidationPerType(): void
    {
        $this->agent->succeed('backup.remote_test', ['ok' => true]);
        $this->post('/backups/storages', ['name' => 'Disk', 'type' => 'local', 'path' => '/etc/whacp']);
        self::assertArrayHasKey('path', (array) $this->flash('errors'));
        $this->post('/backups/storages', ['name' => 'Box', 'type' => 'sftp', 'host' => 'bad host', 'username' => 'u', 'private_key' => 'secret-key-material']);
        self::assertArrayHasKey('host', (array) $this->flash('errors'));
        self::assertArrayNotHasKey('private_key', (array) $this->flash('old'), 'secrets are not flashed back');
        $this->post('/backups/storages', ['name' => 'Box', 'type' => 'ftp', 'host' => 'ftp.example.com', 'username' => 'u']);
        self::assertArrayHasKey('password', (array) $this->flash('errors'));
        self::assertSame(0, $this->calls('backup.remote_test'));

        // A local folder with no trust step is saved straight away.
        $this->post('/backups/storages', ['name' => 'Disk', 'type' => 'local', 'path' => '/mnt/backup/whacp/']);
        self::assertSame('/mnt/backup/whacp', $this->lastCall('backup.remote_test')['remote']['path']);
        self::assertSame('local', $this->db->scalar('SELECT kind FROM backup_storages'));
    }

    public function testGoogleDriveOAuth(): void
    {
        $oauth = new class () extends \WhaCP\Services\OAuthClient {
            /** @var list<array<string, string>> */
            public array $forms = [];
            public function token(array $form): array
            {
                $this->forms[] = $form;
                return ['access_token' => 'at', 'refresh_token' => 'refresh-token-value'];
            }
        };
        $this->app->container->instance(\WhaCP\Services\OAuthClient::class, $oauth);
        $input = ['name' => 'Drive', 'type' => 'gdrive', 'client_id' => 'cid.apps.googleusercontent.com', 'client_secret' => 'client-secret-value'];

        // Google only redirects to https.
        $this->post('/backups/storages', $input);
        self::assertStringContainsString('HTTPS', (string) $this->flash('error'));

        $this->app->container->get(\WhaCP\Services\SettingsService::class)->update(['panel_url' => 'https://panel.example.com'], null);
        $res = $this->post('/backups/storages', $input);
        $loc = (string) $res->header('Location');
        self::assertStringStartsWith('https://accounts.google.com/o/oauth2/v2/auth?', $loc);
        parse_str((string) parse_url($loc, PHP_URL_QUERY), $q);
        self::assertSame(['cid.apps.googleusercontent.com', 'https://panel.example.com/backups/storages/google/callback', 'offline', 'https://www.googleapis.com/auth/drive.file'], [$q['client_id'], $q['redirect_uri'], $q['access_type'], $q['scope']]);
        self::assertStringNotContainsString('client-secret-value', $loc);

        // A forged callback (wrong state) is refused and ends the attempt.
        $this->request('GET', '/backups/storages/google/callback', query: ['state' => 'forged', 'code' => 'c']);
        self::assertStringContainsString('state', (string) $this->flash('error'));
        self::assertSame([], $oauth->forms);

        $res = $this->post('/backups/storages', $input);
        parse_str((string) parse_url((string) $res->header('Location'), PHP_URL_QUERY), $q);
        $this->agent->succeed('backup.remote_test', ['ok' => true, 'folder_id' => 'folder123']);
        $this->request('GET', '/backups/storages/google/callback', query: ['state' => $q['state'], 'code' => 'auth-code']);
        self::assertSame('auth-code', $oauth->forms[0]['code']);
        self::assertSame('client-secret-value', $oauth->forms[0]['client_secret']);
        self::assertSame('refresh-token-value', $this->lastCall('backup.remote_test')['remote']['refresh_token']);
        self::assertSame('gdrive', $this->db->scalar('SELECT kind FROM backup_storages'));
        $id = (int) $this->db->scalar('SELECT id FROM backup_storages');
        $this->post('/sites/' . $this->siteId . '/backups', ['files' => '1', 'storage_id' => (string) $id]);
        $remote = $this->lastCall('backup.create')['remote'];
        self::assertSame(['folder123', 'refresh-token-value'], [$remote['folder_id'], $remote['refresh_token']]);
        self::assertStringNotContainsString('refresh-token-value', (string) json_encode($this->db->fetchAll('SELECT * FROM backup_storages')));
    }

    public function testServerRestore(): void
    {
        $this->db->run("INSERT INTO backups (server_id, kind, backup_id, status, local, created_at) VALUES (1, 'server', '20260901T120000Z-abcdef', 'completed', 1, '2026-09-01')");
        $id = (int) $this->db->scalar("SELECT id FROM backups WHERE kind = 'server'");
        self::assertStringContainsString('Extract for review only', $this->request('GET', '/backups')->body());
        $this->agent->succeed('backup.server_restore', ['job_id' => 'ffffffffffffffff']);

        $this->post('/backups/server/' . $id . '/restore', ['components' => ['nginx']]);
        self::assertStringContainsString('restore', (string) $this->flash('error'));
        self::assertSame(0, $this->calls('backup.server_restore'));

        $this->post('/backups/server/' . $id . '/restore', ['components' => ['nginx', 'ssh', 'php'], 'confirm' => 'restore']);
        $call = $this->lastCall('backup.server_restore');
        self::assertSame(['nginx', 'php'], $call['components'], 'only restorable components are passed');
        self::assertTrue($call['confirm']);
        self::assertFalse($call['extract_only']);

        $this->post('/backups/server/' . $id . '/restore', ['extract_only' => '1']);
        self::assertTrue($this->lastCall('backup.server_restore')['extract_only']);
        self::assertSame([], $this->lastCall('backup.server_restore')['components']);
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'backup.server_extract'"));
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
