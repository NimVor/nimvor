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

    private function widgetHtml(string $id): string
    {
        $res = $this->request('GET', '/dashboard/widgets/' . $id, [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        self::assertSame(200, $res->status(), $res->body());
        return (string) ((array) json_decode($res->body(), true))['html'];
    }

    public function testWebsiteHealthAndStatisticsAndTheWebsitesCardAreOneWidgetInTabs(): void
    {
        $page = $this->request('GET', '/')->body();
        foreach (['website_health', 'websites_summary'] as $gone) {
            self::assertStringNotContainsString('data-widget="' . $gone . '"', $page, $gone . ' is a tab of Website statistics now');
        }
        self::assertStringNotContainsString('data-net-rx', $page, 'the Network card is gone from the dashboard');
        $html = $this->widgetHtml('website_overview');
        self::assertStringContainsString('data-nim-tabs="website-statistics"', $html);
        foreach (['overview', 'slowest', 'created', 'websites'] as $tab) {
            self::assertStringContainsString('data-nim-tab="' . $tab . '"', $html);
            self::assertStringContainsString('data-nim-pane="' . $tab . '"', $html);
        }
        foreach (['Slowest websites', 'Created per month', 'Using HTTPS'] as $text) {
            self::assertStringContainsString($text, $html);
        }
    }

    public function testTopProcessesShowByCpuAndByMemoryInTwoTabs(): void
    {
        $this->agent->succeed('system.live', ['processes' => 9, 'top_cpu' => [['pid' => 5, 'name' => 'cpuhog', 'user' => 'u', 'state' => 'R', 'cpu_percent' => 40.0, 'rss_bytes' => 1048576]], 'top_memory' => [['pid' => 6, 'name' => 'memhog', 'user' => 'u', 'state' => 'S', 'cpu_percent' => 0.0, 'rss_bytes' => 5242880]]]);
        $html = $this->widgetHtml('top_processes');
        self::assertStringContainsString('data-nim-pane="cpu"', $html);
        self::assertStringContainsString('data-nim-pane="mem"', $html);
        self::assertStringContainsString('cpuhog', $html);
        self::assertStringContainsString('memhog', $html);
    }

    public function testTheWideWidgetsAreSizedByTheirNewTabs(): void
    {
        $reg = new \NimVor\Dashboard\WidgetRegistry();
        self::assertSame('md', $reg->find('top_processes')->size);
        self::assertSame('half', $reg->find('traffic_overview')->size);
        self::assertSame('half', $reg->find('request_trends')->size);
        self::assertSame('nim-span-half', \NimVor\Dashboard\Widget::SIZES['half']);
        self::assertNull($reg->find('website_health'));
        self::assertNull($reg->find('websites_summary'));
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
        foreach (['tile_cpu', 'tile_memory', 'tile_disk', 'tile_load', 'software', 'website_overview'] as $id) {
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
