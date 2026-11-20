<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Tests\Support\HttpTestCase;

/** Server → Disk usage: filesystems, a scan of where the space goes, and bulk removal of listed files. */
final class ServerDiskTest extends HttpTestCase
{
    private const JOB = 'abcdef0123456789';

    private function scanResult(): array
    {
        return [
            'scanned_at' => '2026-11-20T10:00:00Z', 'min_bytes' => 52428800,
            'areas' => [
                ['key' => 'sites', 'label' => 'Websites', 'path' => '/home/nimvor', 'bytes' => 9000000000, 'files' => 120000, 'deletable' => true],
                ['key' => 'mail', 'label' => 'Mailboxes', 'path' => '/var/vmail', 'bytes' => 100, 'files' => 3, 'deletable' => false],
            ],
            'sites' => [['domain' => 'shop.test', 'bytes' => 7000000000, 'files' => 100000], ['domain' => 'blog.test', 'bytes' => 2000000000, 'files' => 20000]],
            'large' => [
                ['path' => '/home/nimvor/shop.test/dump.sql', 'bytes' => 3000000000, 'modified' => '2026-10-01T00:00:00Z', 'area' => 'sites', 'site' => 'shop.test', 'deletable' => true],
                ['path' => '/var/vmail/a.test/u/msg', 'bytes' => 60000000, 'modified' => '2026-10-02T00:00:00Z', 'area' => 'mail', 'deletable' => false],
            ],
        ];
    }

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->agent->succeed('disk.filesystems', [['mount' => '/', 'device' => '/dev/vda1', 'type' => 'ext4', 'total_bytes' => 100000000000, 'used_bytes' => 91000000000, 'avail_bytes' => 9000000000, 'used_percent' => 91.0, 'inodes' => 6000000, 'inodes_used' => 600000, 'inodes_percent' => 10.0]]);
        $this->agent->succeed('disk.scan', ['job_id' => self::JOB, 'status' => 'running']);
        $this->agent->succeed('jobs.get', ['id' => self::JOB, 'status' => 'succeeded', 'result' => $this->scanResult()]);
        $this->login('admin');
    }

    public function testTheOverviewShowsTheFilesystemsAndNeverScannedMessage(): void
    {
        $body = $this->request('GET', '/server/disk')->body();
        self::assertStringContainsString('/dev/vda1', $body);
        self::assertStringContainsString('91', $body);
        self::assertStringContainsString('Not measured yet', $body);
    }

    public function testAScanIsStartedAndItsResultShownInTheThreeTabs(): void
    {
        $this->post('/server/disk/scan', ['min_mb' => '50']);
        self::assertSame(['min_mb' => 50], $this->lastCall('disk.scan'));
        $this->post('/server/disk/scan', ['min_mb' => '50']);
        self::assertStringContainsString('already', (string) $this->flash('error'), 'one scan at a time');

        // The job finished: the next visit records the result.
        $overview = $this->request('GET', '/server/disk')->body();
        self::assertStringContainsString('Websites', $overview);
        self::assertStringContainsString('/var/vmail', $overview);
        $sites = $this->request('GET', '/server/disk', [], [], '', ['tab' => 'websites'])->body();
        self::assertStringContainsString('shop.test', $sites);
        $files = $this->request('GET', '/server/disk', [], [], '', ['tab' => 'files'])->body();
        self::assertStringContainsString('/home/nimvor/shop.test/dump.sql', $files);
        self::assertStringContainsString('name="paths[]" value="/var/vmail/a.test/u/msg" disabled', $files, 'mailbox files cannot be selected');
    }

    public function testOnlyFilesFromTheLatestScanAreRemovedAndTheRemovalIsAudited(): void
    {
        $this->post('/server/disk/scan', ['min_mb' => '50']);
        $this->request('GET', '/server/disk');
        $this->agent->succeed('disk.remove', ['freed_bytes' => 3000000000, 'results' => [['path' => '/home/nimvor/shop.test/dump.sql', 'bytes' => 3000000000, 'action' => 'deleted']]]);

        // A path the scan did not list is refused before the agent is asked.
        $this->post('/server/disk/remove', ['paths' => ['/etc/passwd']]);
        self::assertStringContainsString('not in the latest scan', (string) $this->flash('error'));
        self::assertSame(0, $this->calls('disk.remove'));
        // Not removable (mailbox) files are refused too.
        $this->post('/server/disk/remove', ['paths' => ['/var/vmail/a.test/u/msg']]);
        self::assertSame(0, $this->calls('disk.remove'));

        $this->post('/server/disk/remove', ['paths' => ['/home/nimvor/shop.test/dump.sql']]);
        self::assertSame(['paths' => ['/home/nimvor/shop.test/dump.sql']], $this->lastCall('disk.remove'));
        self::assertStringContainsString('1 file(s) removed', (string) $this->flash('success'));
        $db = $this->app->container->get(Connection::class);
        self::assertSame(1, (int) $db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'server.disk.remove'"));
    }
}
