<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\MonitoringService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** The history behind the dashboard charts: what is recorded, what the series endpoint returns, and the widgets that draw it. */
final class ChartHistoryTest extends HttpTestCase
{
    private Connection $conn;
    private MonitoringService $mon;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('plain', 'customer');
        $this->conn = $this->app->container->get(Connection::class);
        $this->mon = $this->app->container->get(MonitoringService::class);
        $this->mon->sender = static fn (): bool => true;
        $this->agent->succeed('system.info', ['system' => ['cpu' => ['cores' => 2]]]);
        $this->agent->succeed('sites.usage', []);
        $this->agent->succeed('discovery.certificates', []);
    }

    private function metrics(array $extra = []): void
    {
        $this->agent->succeed('system.metrics', $extra + [
            'cpu_percent' => 12.5, 'memory' => ['used_percent' => 40.0, 'swap_total_bytes' => 1000, 'swap_used_bytes' => 250],
            'load' => ['load1' => 0.5, 'load5' => 0.4, 'load15' => 0.3], 'disks' => [['mount_point' => '/', 'used_percent' => 10]], 'network' => [],
        ]);
    }

    private function json(string $path): array
    {
        [$p, $q] = array_pad(explode('?', $path, 2), 2, '');
        parse_str($q, $query);
        $res = $this->request('GET', $p, [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest'], '', $query);
        self::assertSame(200, $res->status(), $res->body());
        return (array) json_decode($res->body(), true);
    }

    public function testAMonitoringPassRecordsSwapLoadServicesAndDatabaseActivity(): void
    {
        $this->metrics();
        $this->agent->succeed('services.status', [
            ['unit' => 'nginx.service', 'unit_file_state' => 'enabled', 'active_state' => 'active'],
            ['unit' => 'mariadb.service', 'unit_file_state' => 'enabled', 'active_state' => 'failed'],
            ['unit' => 'cron.service', 'unit_file_state' => 'disabled', 'active_state' => 'inactive'],
        ]);
        $this->agent->succeed('db.server_status', ['running' => true, 'status' => ['Threads_connected' => '7', 'Questions' => '1000']]);
        $t = time() - 600;
        $this->mon->run($t);
        $row = (array) $this->conn->fetch('SELECT * FROM metric_samples ORDER BY id DESC LIMIT 1');
        self::assertEquals(25.0, $row['swap']);
        self::assertEquals(0.4, $row['load5']);
        self::assertEquals(0.3, $row['load15']);
        self::assertSame([1, 2], [(int) $row['svc_up'], (int) $row['svc_total']], 'only enabled services count');
        self::assertSame([7, 1000], [(int) $row['db_conn'], (int) $row['db_q']]);
        self::assertNull($row['db_qps'], 'no rate from a single sample');

        $this->agent->succeed('db.server_status', ['running' => true, 'status' => ['Threads_connected' => '9', 'Questions' => '1600']]);
        $this->mon->run($t + 300);
        self::assertEquals(2.0, (float) $this->conn->scalar('SELECT db_qps FROM metric_samples ORDER BY id DESC LIMIT 1'), '600 statements in 300 s');

        // The server restarted: its counter went back, so no rate is invented.
        $this->agent->succeed('db.server_status', ['running' => true, 'status' => ['Threads_connected' => '1', 'Questions' => '50']]);
        $this->mon->run($t + 600);
        self::assertNull($this->conn->scalar('SELECT db_qps FROM metric_samples ORDER BY id DESC LIMIT 1'));
    }

    public function testWhatCannotBeMeasuredIsNullNeverZero(): void
    {
        $this->metrics(['memory' => ['used_percent' => 40.0, 'swap_total_bytes' => 0, 'swap_used_bytes' => 0], 'load' => ['load1' => 0.5]]);
        // No services.status and no db.server_status answer in the fake agent.
        $this->mon->run();
        $row = (array) $this->conn->fetch('SELECT * FROM metric_samples ORDER BY id DESC LIMIT 1');
        foreach (['swap', 'load5', 'load15', 'svc_up', 'svc_total', 'db_conn', 'db_qps', 'db_q'] as $col) {
            self::assertNull($row[$col], $col . ' was not measured');
        }
    }

    public function testTheSeriesEndpointReturnsOnlyRecordedValuesWithGaps(): void
    {
        $now = time();
        foreach ([[3 * 3600, 20.0, 30.0, null], [2 * 3600, 22.0, 31.0, 10], [1 * 3600, 24.0, 32.0, 12]] as [$ago, $cpu, $mem, $conn]) {
            $this->conn->run('INSERT INTO metric_samples (server_id, taken_at, cpu, mem, disk, load1, rx_bps, tx_bps, db_conn) VALUES (1, ?, ?, ?, 10, 0.5, 100, 200, ?)', [$now - $ago, $cpu, $mem, $conn]);
        }
        $this->login('admin');
        $r = $this->json('/dashboard/series?range=6h');
        self::assertSame('6h', $r['range']);
        self::assertCount(3, $r['points']);
        self::assertNull($r['points'][0]['db_conn'], 'a period without a measurement is a gap');
        self::assertNull($r['points'][0]['swap'], 'swap was never recorded');
        self::assertEquals(24.0, $r['points'][2]['cpu']);
        self::assertSame($now - 3 * 3600, $r['since']);

        $narrow = $this->json('/dashboard/series?range=1h');
        self::assertSame([], array_filter($narrow['points'], static fn (array $p): bool => $p['t'] < $narrow['from']), 'only the asked range');
        self::assertSame('24h', $this->json('/dashboard/series?range=bogus')['range'], 'an unknown range falls back to 24 hours');
    }

    public function testTheSeriesEndpointIsForThoseWhoMayViewMonitoring(): void
    {
        $this->login('plain');
        $res = $this->request('GET', '/dashboard/series', [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest'], '', ['range' => '24h']);
        self::assertNotSame(200, $res->status());
        self::assertStringNotContainsString('"points"', $res->body());
    }

    public function testThePerformanceWidgetShowsLiveReadingsAndMountsTheChart(): void
    {
        $this->agent->succeed('system.metrics', [
            'cpu_percent' => 41.2, 'memory' => ['used_percent' => 62.0, 'used_bytes' => 6000000000, 'available_bytes' => 3000000000, 'total_bytes' => 9000000000, 'swap_total_bytes' => 2000000000, 'swap_used_bytes' => 500000000],
            'load' => ['load1' => 1.5, 'load5' => 1.2, 'load15' => 0.9], 'network' => [['rx_bytes_per_sec' => 2048, 'tx_bytes_per_sec' => 1024]],
        ]);
        $this->login('admin');
        $w = $this->json('/dashboard/widgets/performance_history');
        self::assertSame('ok', $w['state']);
        foreach (['41%', '62%', 'of 1.9 GB', '1.50', '5 min 1.20', '2 KB/s', 'data-nim-chart', 'data-metric="cpu"', 'data-chart-tab="net"', 'data-range-button="7d"', '/dashboard/series'] as $s) {
            self::assertStringContainsString($s, $w['html']);
        }
        // Without an answer from the agent nothing is drawn as zero.
        $this->conn->run('DELETE FROM widget_cache');
        $this->agent->fail('system.metrics', 500, 'failed', 'down');
        self::assertSame('unavailable', $this->json('/dashboard/widgets/performance_history')['state']);
    }

    public function testTheWebsiteOverviewCountsStatesAndMonthsFromRealRecords(): void
    {
        $admin = (array) $this->app->container->get(UserRepository::class)->find(1);
        $sites = $this->app->container->get(WebsiteService::class);
        $ids = [];
        foreach (['a.test', 'b.test', 'c.test'] as $d) {
            $ids[$d] = $sites->create(['domain' => $d, 'kind' => 'static'], $admin);
        }
        $this->conn->run('UPDATE websites SET enabled = 0 WHERE id = ?', [$ids['b.test']]);
        $this->conn->run('UPDATE websites SET https = 1 WHERE id = ?', [$ids['a.test']]);
        $this->conn->run('UPDATE websites SET created_at = ? WHERE id = ?', [gmdate('c', strtotime('first day of -2 month')), $ids['c.test']]);
        $this->login('admin');
        $html = $this->json('/dashboard/widgets/website_overview')['html'];
        self::assertStringContainsString('>3<', $html, 'the donut says how many websites');
        self::assertMatchesRegularExpression('#<title>Active: 2 \(67%\)</title>#', $html);
        self::assertMatchesRegularExpression('#<title>Disabled: 1 \(33%\)</title>#', $html);
        self::assertStringNotContainsString('Suspended: ', $html, 'a state without websites has no segment');
        self::assertSame(6, substr_count($html, 'class="nim-bar"'), 'the last six months');
        self::assertStringContainsString('Request trends are not collected', $html);
    }

    public function testTheServicesOverviewCountsEnabledServicesOnceAndAdmitsMissingHistory(): void
    {
        $this->agent->succeed('services.status', [
            ['unit' => 'nginx.service', 'unit_file_state' => 'enabled', 'active_state' => 'active'],
            ['unit' => 'nginx.service', 'unit_file_state' => 'enabled', 'active_state' => 'active'],
            ['unit' => 'mariadb.service', 'unit_file_state' => 'enabled', 'active_state' => 'failed'],
            ['unit' => 'redis.service', 'unit_file_state' => 'enabled', 'active_state' => 'inactive'],
            ['unit' => 'cups.service', 'unit_file_state' => 'disabled', 'active_state' => 'inactive'],
        ]);
        $this->login('admin');
        $html = $this->json('/dashboard/widgets/services_overview')['html'];
        self::assertStringContainsString('aria-label="1 running, 1 stopped, 1 failed"', $html);
        self::assertStringContainsString('mariadb, redis', $html);
        self::assertStringNotContainsString('cups', $html);
        self::assertStringContainsString('data-metric="svc"', $html);
    }

    public function testTheDiskWidgetDrawsADonutPerFilesystemAndTheDatabaseWidgetOnlyChartsAServerThatRuns(): void
    {
        $this->agent->succeed('disk.filesystems', [
            ['mount' => '/', 'device' => '/dev/vda1', 'type' => 'ext4', 'total_bytes' => 100000000000, 'used_bytes' => 25000000000, 'avail_bytes' => 75000000000, 'used_percent' => 25.0, 'inodes' => 100, 'inodes_used' => 10, 'inodes_percent' => 10.0],
            ['mount' => '/data', 'device' => '/dev/vdb', 'type' => 'ext4', 'total_bytes' => 200000000000, 'used_bytes' => 190000000000, 'avail_bytes' => 10000000000, 'used_percent' => 95.0, 'inodes' => 100, 'inodes_used' => 10, 'inodes_percent' => 10.0],
        ]);
        $this->agent->succeed('db.server_status', ['running' => false, 'unit' => 'mariadb', 'error' => 'down']);
        $this->agent->succeed('pg.status', ['installed' => false]);
        $this->login('admin');
        $disk = $this->json('/dashboard/widgets/disk')['html'];
        self::assertSame(2, substr_count($disk, 'class="nim-donut"'));
        self::assertStringContainsString('nim-donut-seg c-ok', $disk);
        self::assertStringContainsString('nim-donut-seg c-bad', $disk);
        self::assertStringContainsString('data-fixed-range="7d"', $disk);
        self::assertStringNotContainsString('data-metric="db_conn"', $this->json('/dashboard/widgets/database_overview')['html'], 'a stopped database has no history to draw');
    }
}
