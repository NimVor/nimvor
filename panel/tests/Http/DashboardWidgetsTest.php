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
        self::assertStringContainsString('nim-donut-seg c-bad', $page, 'a nearly full disk is a red donut');
        self::assertStringContainsString('>91%<', $page, 'the donut says how full it is');
        self::assertStringContainsString('Scan the disk on the Disk usage page', $page);
    }

    public function testTheSeparateBackupsWidgetIsGone(): void
    {
        $page = $this->request('GET', '/')->body();
        self::assertStringNotContainsString('data-widget="backups"', $page, 'Backup health already covers it');
        self::assertStringContainsString('data-widget="backup_health"', $page);
    }

    public function testSoftwareTilesOfDnsMailCachesWebAndCertbotOpenDetailsOnHover(): void
    {
        $this->agent->succeed('components.status', [
            ['id' => 'dns', 'name' => 'DNS server', 'installed' => true, 'units' => ['pdns'], 'steps' => [['id' => 'running', 'status' => 'ok']]],
            ['id' => 'database', 'name' => 'Database server', 'installed' => true, 'units' => ['mariadb'], 'steps' => [['id' => 'running', 'status' => 'ok']]],
            ['id' => 'php', 'name' => 'PHP', 'installed' => true, 'units' => ['php8.3-fpm'], 'steps' => [['id' => 'running', 'status' => 'ok']]],
        ]);
        $page = $this->request('GET', '/dashboard/widgets/software', [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        $html = (string) ((array) json_decode($page->body(), true))['html'];
        self::assertStringContainsString('data-popover-url="/dashboard/popover/dns"', $html);
        self::assertStringNotContainsString('/dashboard/popover/php', $html, 'PHP has no hover card');
        self::assertStringContainsString('/databases/server/settings/modal', $html, 'the database tile\'s settings open in a window');
        self::assertStringContainsString('data-nim-site-modal', $html);
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

    public function testTheTopCardsAreWidgetsDrawnWithThePage(): void
    {
        $page = $this->request('GET', '/')->body();
        foreach (['tile_cpu', 'tile_memory', 'tile_disk', 'tile_load', 'software', 'websites_summary'] as $id) {
            self::assertStringContainsString('data-widget-host="' . $id . '"', $page, $id);
        }
        self::assertStringContainsString('data-metric', $page, 'the live poll still finds the cards');
    }

    public function testRequestTrendsAddUpTheSitesAndSayNotAvailableWithoutLogs(): void
    {
        $db = $this->app->container->get(Connection::class);
        $db->run("INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, 'shop.test', 'static', '/h/s', '/h/s/p', 'u1', 1, 'active', 'x', 'x')");
        $now = intdiv(time(), 3600) * 3600;
        $this->agent->succeed('webreport.site', [
            'exists' => true, 'truncated' => false, 'hours' => 48, 'requests' => 900, 'refused' => 10,
            'classes' => [0, 0, 800, 50, 40, 10],
            'timeline' => [['start' => $now - 7200, 'requests' => 100, 'refused' => 0], ['start' => $now - 3600, 'requests' => 200, 'refused' => 10]],
        ]);
        $res = $this->request('GET', '/dashboard/widgets/request_trends', [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        $w = (array) json_decode($res->body(), true);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame('ok', $w['state']);
        self::assertStringContainsString('shop.test', $w['html']);
    }

    public function testRequestTrendsAreNotAvailableWhenNothingCanBeRead(): void
    {
        $res = $this->request('GET', '/dashboard/widgets/request_trends', [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        $w = (array) json_decode($res->body(), true);
        self::assertSame(200, $res->status(), $res->body());
        self::assertNotSame('ok', $w['state']);
    }
}
