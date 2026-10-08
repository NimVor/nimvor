<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\BackupService;
use WhaCP\Services\WebsiteService;
use WhaCP\Support\UserFacingException;
use WhaCP\Tests\Support\HttpTestCase;

/**
 * Backups for administrators: remote storages turned on only after a
 * passing test, edited and tested; the backup key downloaded (protected by
 * a passphrase, after the panel password); account backups made and
 * restored after the account was terminated.
 */
final class BackupsAdminTest extends HttpTestCase
{
    private Connection $db;
    private int $admin;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('backup.key_info', ['exists' => true, 'key_id' => 'abcd', 'path' => '/var/lib/whacp-agent/backup.key']);
        $this->agent->succeed('backup.account_files', ['files' => []]);
    }

    private function storage(string $name, array $cfg): int
    {
        return $this->app->container->get(BackupService::class)->insertStorage($name, $cfg);
    }

    public function testStorageOnOnlyAfterAPassingTestAndEdit(): void
    {
        $this->login('admin');
        $id = $this->storage('Disk', ['type' => 'local', 'path' => '/mnt/disk', 'prefix' => '']);
        $svc = $this->app->container->get(BackupService::class);
        self::assertTrue($svc->storages()[0]['active'], 'a storage added after its test is on');

        $this->post('/backups/storages/' . $id . '/active', ['active' => '0']);
        self::assertSame([], $svc->storages(true));
        self::assertSame('/backups#storages', $this->lastLocation());
        try {
            $svc->activeStorageConfig($id);
            self::fail('a storage turned off was used for a new backup');
        } catch (UserFacingException) {
        }

        // A failing test turns it off and keeps it off.
        $this->agent->fail('backup.remote_test', 422, 'failed', 'connection refused');
        $this->post('/backups/storages/' . $id . '/test');
        $row = $this->db->fetch('SELECT * FROM backup_storages WHERE id = ?', [$id]);
        self::assertSame(0, (int) $row['test_ok']);
        self::assertStringContainsString('connection refused', (string) $row['test_error']);
        $this->post('/backups/storages/' . $id . '/active', ['active' => '1']);
        self::assertSame(0, (int) $this->db->scalar('SELECT active FROM backup_storages WHERE id = ?', [$id]));

        // A passing test allows it again.
        $this->agent->succeed('backup.remote_test', ['ok' => true]);
        $this->post('/backups/storages/' . $id . '/test');
        $this->post('/backups/storages/' . $id . '/active', ['active' => '1']);
        self::assertSame(1, (int) $this->db->scalar('SELECT active FROM backup_storages WHERE id = ?', [$id]));

        // Edit: tested with the new settings before they are saved.
        $this->post('/backups/storages/' . $id, ['name' => 'Disk 2', 'path' => '/mnt/disk2', 'prefix' => 'web1']);
        self::assertSame('/mnt/disk2', $this->lastCall('backup.remote_test')['remote']['path']);
        $s = $svc->storages()[0];
        self::assertSame('Disk 2', $s['name']);
        self::assertSame('web1', $s['prefix']);

        // The server of an SFTP storage is not changed (its key was confirmed).
        $sftp = $this->storage('Box', ['type' => 'sftp', 'host' => 'box.example.test', 'port' => 22, 'username' => 'u', 'password' => 'secret', 'private_key' => '', 'host_key' => 'ssh-ed25519 AAAA', 'prefix' => '']);
        $this->post('/backups/storages/' . $sftp, ['name' => 'Box', 'host' => 'evil.example.test', 'port' => '22', 'username' => 'u']);
        self::assertSame('box.example.test', $svc->storageConfig($sftp)['host']);
        // Changing the user name keeps the stored password and pinned key.
        $this->post('/backups/storages/' . $sftp, ['name' => 'Box', 'port' => '22', 'username' => 'backup']);
        $cfg = $svc->storageConfig($sftp);
        self::assertSame('backup', $cfg['username']);
        self::assertSame('secret', $cfg['password']);
        self::assertSame('ssh-ed25519 AAAA', $cfg['host_key']);
    }

    public function testLocalFolderChoices(): void
    {
        $this->login('admin');
        $this->agent->succeed('backup.remote_test', ['ok' => true, 'created' => true]);
        // /backups itself is accepted (the agent creates it when missing).
        $this->post('/backups/storages', ['name' => 'Local', 'type' => 'local', 'path' => '/backups', 'prefix' => '']);
        self::assertSame('/backups', $this->lastCall('backup.remote_test')['remote']['path']);
        self::assertSame(1, (int) $this->app->container->get(\WhaCP\Database\Connection::class)->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'backup.storage_folder_create'"));
        // A mount root or a folder elsewhere is refused before the agent is asked.
        foreach (['/mnt', '/media', '/etc/backups', '/backups/../etc'] as $i => $p) {
            $this->post('/backups/storages', ['name' => 'Bad' . $i, 'type' => 'local', 'path' => $p, 'prefix' => '']);
        }
        self::assertSame(1, $this->calls('backup.remote_test'));
    }

    public function testKeyDownloadNeedsAnAdministratorAndThePanelPassword(): void
    {
        $this->agent->succeed('backup.key_export', ['key_id' => 'abcd', 'file' => "{\"format\":\"whacp-backup-key\"}\n"]);
        $this->seedUser('op', 'operator');
        $this->login('op');
        $this->post('/backups/key/download', ['passphrase' => 'long enough passphrase', 'passphrase_confirmation' => 'long enough passphrase', 'current_password' => 'a-long-test-password']);
        self::assertSame(0, $this->calls('backup.key_export'), 'only administrators');

        $this->post('/logout');
        $this->login('admin');
        $this->post('/backups/key/download', ['passphrase' => 'long enough passphrase', 'passphrase_confirmation' => 'long enough passphrase', 'current_password' => 'wrong']);
        self::assertSame(0, $this->calls('backup.key_export'));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'backup.key_export' AND result = 'denied'"));
        $this->post('/backups/key/download', ['passphrase' => 'short', 'passphrase_confirmation' => 'short', 'current_password' => 'a-long-test-password']);
        self::assertSame(0, $this->calls('backup.key_export'));

        $res = $this->post('/backups/key/download', ['passphrase' => 'long enough passphrase', 'passphrase_confirmation' => 'long enough passphrase', 'current_password' => 'a-long-test-password']);
        self::assertSame(200, $res->status());
        self::assertStringContainsString('attachment; filename="whacp-backup-key-abcd.json"', (string) $res->header('Content-Disposition'));
        self::assertSame('no-store', $res->header('Cache-Control'));
        self::assertStringContainsString('whacp-backup-key', $res->body());
        self::assertSame('long enough passphrase', $this->lastCall('backup.key_export')['passphrase']);
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'backup.key_export' AND result = 'success'"));
    }

    public function testAccountBackupAndRestoreAfterTermination(): void
    {
        $this->login('admin');
        $carol = $this->seedUser('carol', 'customer');
        $carolUser = (array) $this->app->container->get(UserRepository::class)->find($carol);
        $siteId = $this->app->container->get(WebsiteService::class)->create(['domain' => 'carol.test', 'kind' => 'static', 'system_user' => 'carol', 'new_system_user' => true], $carolUser);
        $this->db->run("INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (1, ?, ?, 'carol_shop', '2026-01-01')", [$siteId, $carol]);

        // Back up the account.
        $this->agent->succeed('backup.account', ['job_id' => 'aaaaaaaaaaaaaaaa']);
        $this->post('/backups/accounts', ['user_id' => (string) $carol, 'label' => 'before']);
        $call = $this->lastCall('backup.account');
        self::assertSame('carol', $call['username']);
        self::assertSame('carol', $call['meta']['user']['username']);
        self::assertSame('carol.test', $call['meta']['websites'][0]['domain']);
        self::assertSame(['carol_shop'], $call['sites'][0]['databases']);
        self::assertSame('carol', $call['sites'][0]['user']);
        $meta = $call['meta'];

        // Terminated: the website, database and user are gone.
        $this->db->run('DELETE FROM databases');
        $this->db->run('DELETE FROM websites');
        $this->db->run("DELETE FROM users WHERE username = 'carol'");

        $path = '/var/backups/whacp/accounts/carol/carol-20260901T030000Z-a1b2c3.whacp-account';
        $manifest = ['version' => 1, 'id' => '20260901T030000Z-a1b2c3', 'username' => 'carol', 'key_id' => 'abcd', 'created_at' => '2026-09-01T03:00:00Z', 'meta' => $meta, 'sites' => [['domain' => 'carol.test', 'id' => '20260901T030000Z-d4e5f6']]];
        $this->agent->succeed('backup.account_inspect', ['manifest' => $manifest]);
        $page = $this->request('GET', '/backups/accounts/restore', [], [], '', ['path' => $path])->body();
        self::assertStringContainsString('will be created', $page);

        $this->agent->succeed('backup.account_import', ['job_id' => 'bbbbbbbbbbbbbbbb']);
        $this->post('/backups/accounts/restore', ['path' => $path, 'confirm' => 'wrong']);
        self::assertSame(0, $this->calls('backup.account_import'), 'the account name must be typed');
        $this->post('/backups/accounts/restore', ['path' => $path, 'confirm' => 'carol']);
        self::assertSame($path, $this->lastCall('backup.account_import')['path']);
        $rid = (int) $this->db->scalar('SELECT id FROM account_restores');

        $this->agent->succeed('jobs.get', ['id' => 'bbbbbbbbbbbbbbbb', 'status' => 'running']);
        self::assertStringContainsString('importing its website backups', $this->request('GET', '/backups/accounts/restores/' . $rid)->body());
        $this->agent->succeed('jobs.get', ['id' => 'bbbbbbbbbbbbbbbb', 'status' => 'succeeded', 'result' => []]);
        self::assertStringContainsString('Recreate the account', $this->request('GET', '/backups/accounts/restores/' . $rid)->body());

        // Recreate: account, website, database, then the website backup is restored.
        $this->agent->succeed('db.create', ['created' => 'carol_shop']);
        $this->agent->succeed('backup.list', []);
        $this->agent->succeed('backup.inspect', ['id' => '20260901T030000Z-d4e5f6', 'kind' => 'site', 'domain' => 'carol.test', 'encrypted' => true, 'local' => true, 'size' => 10,
            'files' => [['name' => 'files.tar.gz.enc', 'kind' => 'files', 'size' => 5, 'sha256' => 'x'], ['name' => 'db-carol_shop.sql.gz.enc', 'kind' => 'database', 'database' => 'carol_shop', 'size' => 5, 'sha256' => 'y']]]);
        $this->agent->succeed('backup.restore', ['job_id' => 'cccccccccccccccc']);
        $this->post('/backups/accounts/restores/' . $rid . '/recreate');

        $user = $this->db->fetch("SELECT u.id, r.name AS role FROM users u JOIN roles r ON r.id = u.role_id WHERE username = 'carol'");
        self::assertNotNull($user, 'the account was recreated');
        self::assertSame('customer', $user['role']);
        self::assertSame((int) $user['id'], (int) $this->db->scalar("SELECT owner_id FROM websites WHERE domain = 'carol.test'"));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM databases WHERE name = 'carol_shop'"));
        $restore = $this->lastCall('backup.restore');
        self::assertSame('20260901T030000Z-d4e5f6', $restore['id']);
        self::assertTrue($restore['files']);
        self::assertSame(['carol_shop' => 'carol_shop'], $restore['databases']);
        self::assertSame('done', $this->db->scalar('SELECT status FROM account_restores WHERE id = ?', [$rid]));

        // The new password is shown once.
        $page = $this->request('GET', '/backups/accounts/restores/' . $rid)->body();
        self::assertStringContainsString('not shown again', $page);
        self::assertStringNotContainsString('not shown again', $this->request('GET', '/backups/accounts/restores/' . $rid)->body());
        // Nothing secret is stored in the report.
        self::assertStringNotContainsString('password', strtolower((string) $this->db->scalar('SELECT report FROM account_restores WHERE id = ?', [$rid])));
        // A second recreate is refused.
        $this->post('/backups/accounts/restores/' . $rid . '/recreate');
        self::assertSame(1, $this->calls('backup.restore'));
    }

    public function testAccountBackupsAreForAdministratorsOnly(): void
    {
        $this->seedUser('dave', 'reseller');
        $this->login('dave');
        $this->agent->succeed('backup.account', ['job_id' => 'aaaaaaaaaaaaaaaa']);
        $this->post('/backups/accounts', ['user_id' => '1']);
        $this->post('/backups/accounts/restore', ['path' => '/var/backups/whacp/incoming/x.whacp-account', 'confirm' => 'x']);
        self::assertSame(0, $this->calls('backup.account'));
        self::assertSame(0, $this->calls('backup.account_import'));
    }

    private function lastLocation(): string
    {
        $flashLess = $this->post('/backups/storages/0/active', ['active' => '0']);
        return (string) $flashLess->header('Location');
    }
}
