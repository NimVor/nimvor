<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Tests\Support\HttpTestCase;

/** The administrator dashboard's widgets: disk, backups and what needs attention. */
final class DashboardWidgetsTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->agent->succeed('system.info', ['system' => ['hostname' => 'srv1'], 'os' => ['pretty_name' => 'Ubuntu', 'supported' => true]]);
        $this->agent->succeed('disk.filesystems', [['mount' => '/', 'device' => '/dev/vda1', 'type' => 'ext4', 'total_bytes' => 100000000000, 'used_bytes' => 91000000000, 'avail_bytes' => 9000000000, 'used_percent' => 91.0, 'inodes' => 100, 'inodes_used' => 10, 'inodes_percent' => 10.0]]);
        $this->login('admin');
    }

    public function testTheDiskWidgetShowsFilesystemsAndAHintToScan(): void
    {
        $page = $this->request('GET', '/')->body();
        self::assertStringContainsString('data-widget="disk"', $page);
        self::assertStringContainsString('progress-bar bg-danger', $page, 'a nearly full disk is red');
        self::assertStringContainsString('Scan the disk on the Disk usage page', $page);
    }

    public function testTheBackupsWidgetCountsCompletedFailedAndRunning(): void
    {
        $db = $this->app->container->get(Connection::class);
        $now = gmdate('c');
        foreach ([['completed', $now], ['completed', $now], ['failed', $now], ['running', $now], ['failed', gmdate('c', time() - 20 * 86400)]] as [$status, $at]) {
            $db->run("INSERT INTO backups (server_id, kind, domain, status, created_at) VALUES (1, 'site', 'a.test', ?, ?)", [$status, $at]);
        }
        $page = $this->request('GET', '/')->body();
        self::assertStringContainsString('data-widget="backups"', $page);
        self::assertMatchesRegularExpression('#nim-fig-value">2</div><div class="nim-fig-label">Completed#', $page);
        self::assertMatchesRegularExpression('#nim-fig-value text-danger">1</div><div class="nim-fig-label">Failed \(7 days\)#', $page, 'only failures of the last week count');
        self::assertMatchesRegularExpression('#nim-fig-value">1</div><div class="nim-fig-label">Running#', $page);
    }

    public function testTheLatestDiskScanListsTheBiggestPlaces(): void
    {
        $db = $this->app->container->get(Connection::class);
        $result = ['scanned_at' => gmdate('c'), 'areas' => [
            ['key' => 'sites', 'label' => 'Websites', 'path' => '/home/nimvor', 'bytes' => 9000000000, 'files' => 1],
            ['key' => 'logs', 'label' => 'System and service logs', 'path' => '/var/log', 'bytes' => 500000000, 'files' => 1],
        ]];
        $db->run("INSERT INTO server_disk_scans (server_id, job_id, status, result, started_at, finished_at) VALUES (1, 'abcdef0123456789', 'done', ?, ?, ?)", [json_encode($result), gmdate('c'), gmdate('c')]);
        $page = $this->request('GET', '/')->body();
        self::assertStringContainsString('Biggest places', $page);
        self::assertStringContainsString('System and service logs', $page);
    }
}
