<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\ArraySession;
use NimVor\Services\ApiTokenService;
use NimVor\Services\SiteResourceService;
use NimVor\Tests\Support\HttpTestCase;

/** Website resource monitoring: what is recorded, what is not, how it is aggregated and who may see it. */
final class SiteResourcesTest extends HttpTestCase
{
    private Connection $db;
    private int $shop;
    private int $blog;
    private int $adminId;
    private const T0 = 1_800_000_000; // a multiple of 3600

    protected function setUp(): void
    {
        parent::setUp();
        $this->adminId = $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $other = $this->seedUser('other', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        foreach ([['shop.test', $cust], ['blog.test', $other]] as [$d, $o]) {
            $this->db->run("INSERT INTO websites (server_id, domain, kind, php_version, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, ?, 'php', '8.3', ?, ?, ?, ?, 'active', 'x', 'x')", [$d, '/h/' . $d, '/h/' . $d . '/p', 'u' . $o, $o]);
        }
        $this->shop = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'shop.test'");
        $this->blog = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'blog.test'");
        $this->db->run('UPDATE websites SET php_settings = ? WHERE id = ?', ['{"max_children":4}', $this->shop]);
    }

    private function svc(): SiteResourceService
    {
        return $this->app->container->get(SiteResourceService::class);
    }

    /** @param list<array<string, mixed>> $sites @param list<array<string, mixed>> $accounts */
    private function answer(float $interval, array $sites, array $accounts = []): void
    {
        $this->agent->succeed('sites.metrics', ['taken_at' => 0, 'interval_seconds' => $interval, 'cgroup_v2' => true, 'sites' => $sites, 'accounts' => $accounts]);
    }

    /** @return array<string, mixed> */
    private static function php(int $workers, int $mem, float $cpu, int $rb = 0, int $wb = 0): array
    {
        return ['workers' => $workers, 'memory_bytes' => $mem, 'memory_kind' => 'pss', 'cpu_seconds' => $cpu, 'read_bytes' => $rb, 'write_bytes' => $wb, 'workers_exited' => 0, 'workers_started' => 0];
    }

    /** @return array<string, mixed> */
    private static function web(int $req, int $s4, int $s5, int $bytes): array
    {
        return ['requests' => $req, 'status_2xx' => $req - $s4 - $s5, 'status_3xx' => 0, 'status_4xx' => $s4, 'status_5xx' => $s5, 'bytes_sent' => $bytes];
    }

    /** @param array<string, int> $o @return array<string, mixed> */
    private static function slice(array $o = []): array
    {
        return ['account' => 'acct1', 'users' => ['u1'], 'limits' => ['cpu_percent' => 100, 'memory_mb' => 512, 'tasks_max' => 100],
            'usage' => ['active' => true, 'memory_bytes' => 100 << 20, 'tasks' => 10, 'cpu_usage_usec' => 1_000_000, 'cpu_throttled_usec' => 0, 'cpu_nr_throttled' => 0, 'memory_high_events' => 0, 'memory_max_events' => 0, 'oom_kills' => 0,
                'memory_peak_known' => true, 'memory_peak_bytes' => 200 << 20, 'io_read_bytes' => 0, 'io_write_bytes' => 0, 'io_read_ops' => 0, 'io_write_ops' => 0] + [] ] + [];
    }

    public function testTheFirstSampleStartsCountersAndRatesFollow(): void
    {
        $this->answer(0, [['domain' => 'shop.test', 'user' => 'u1', 'account' => 'acct1', 'php' => self::php(3, 90 << 20, 0)]]);
        $this->svc()->collect(self::T0);
        $row = $this->db->fetch('SELECT * FROM site_metric_samples WHERE website_id = ?', [$this->shop]);
        self::assertSame(3, (int) $row['workers']);
        self::assertSame(90 << 20, (int) $row['mem_bytes']);
        foreach (['cpu_s', 'read_b', 'write_b', 'req', 's4', 's5', 'bytes_sent'] as $c) {
            self::assertNull($row[$c], $c . ' has no rate on the first sample: NULL, not zero');
        }
        $this->answer(60, [['domain' => 'shop.test', 'user' => 'u1', 'account' => 'acct1', 'php' => self::php(2, 80 << 20, 6.0, 6000, 12000), 'web' => self::web(120, 10, 2, 600000)]]);
        $this->svc()->collect(self::T0 + 60);
        $row = $this->db->fetch('SELECT * FROM site_metric_samples WHERE website_id = ? ORDER BY taken_at DESC LIMIT 1', [$this->shop]);
        self::assertEquals([6.0, 6000, 12000, 120, 10, 2, 600000], [$row['cpu_s'], $row['read_b'], $row['write_b'], $row['req'], $row['s4'], $row['s5'], $row['bytes_sent']]);
    }

    public function testASiteWithNothingMeasuredGetsNoRowAndForeignDomainsAreIgnored(): void
    {
        $this->answer(60, [['domain' => 'shop.test'], ['domain' => 'unknown.test', 'php' => self::php(1, 1, 1)]]);
        $this->svc()->collect(self::T0);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM site_metric_samples'));
    }

    public function testAnOldAgentIsHandledGracefully(): void
    {
        $this->agent->fail('sites.metrics', 400, 'unknown_action', 'Unknown action sites.metrics');
        $lines = $this->svc()->collect(self::T0);
        self::assertStringContainsString('not collected', implode(' ', $lines));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM site_metric_samples'));
    }

    public function testSeriesAggregatesFromRealSamples(): void
    {
        foreach ([[0, 0.0], [60, 6.0], [120, 3.0]] as [$dt, $cpu]) {
            $this->answer($dt === 0 ? 0 : 60, [['domain' => 'shop.test', 'account' => 'a', 'php' => self::php(2, 100 << 20, $cpu, 1000, 2000), 'web' => $dt === 0 ? null : self::web(60, 6, 0, 30000)]]);
            $this->svc()->collect(self::T0 + $dt);
        }
        $pts = $this->svc()->series($this->shop, '1h', self::T0 + 130);
        self::assertNotEmpty($pts);
        $sum = static fn (string $k): float => array_sum(array_map(static fn (array $p): float => (float) ($p[$k] ?? 0), $pts));
        // 9 CPU seconds over 120 s of measured interval = 7.5% of a core (the first sample has no rate and is not counted as 0).
        $cpuSeconds = $sum('cpu_seconds');
        self::assertEqualsWithDelta(9.0, $cpuSeconds, 0.01);
        $withCpu = array_values(array_filter($pts, static fn (array $p): bool => $p['cpu'] !== null));
        self::assertEqualsWithDelta(7.5, array_sum(array_column($withCpu, 'cpu')) / count($withCpu), 1.0);
        $first = $pts[0];
        self::assertNull($first['rpm'], 'no requests were measured in the first sample: a gap');
        self::assertSame(100 << 20, $first['mem']);
        $last = end($pts);
        self::assertEqualsWithDelta(60.0, (float) $last['rpm'], 0.5, '60 requests in 60 s of log = 60 per minute');
        self::assertEqualsWithDelta(500.0, (float) $last['bw_out'], 1.0);
        self::assertEqualsWithDelta(6.0, (float) $last['e4'], 0.1);
    }

    public function testHourlyRollupsAndRetention(): void
    {
        foreach ([0, 60, 120] as $dt) {
            $this->answer($dt === 0 ? 0 : 60, [['domain' => 'shop.test', 'php' => self::php(2, 100 << 20, 6.0), 'web' => self::web(60, 0, 0, 1000)]]);
            $this->svc()->collect(self::T0 + $dt);
        }
        $h = $this->db->fetch('SELECT * FROM site_metric_hourly WHERE website_id = ?', [$this->shop]);
        self::assertSame(self::T0, (int) $h['bucket']);
        self::assertEqualsWithDelta(12.0, (float) $h['cpu_s'], 0.001);
        self::assertEqualsWithDelta(120.0, (float) $h['secs_php'], 0.001);
        self::assertSame(120, (int) $h['req']);
        $pts = $this->svc()->series($this->shop, '7d', self::T0 + 3600);
        self::assertEqualsWithDelta(10.0, (float) end($pts)['cpu'], 0.01, '12 CPU seconds over 120 s measured');
        // Retention: raw samples go after resource_raw_days, roll-ups after resource_hourly_days.
        $this->svc()->prune(self::T0 + 4 * 86400);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM site_metric_samples'));
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM site_metric_hourly'));
        $this->svc()->prune(self::T0 + 100 * 86400);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM site_metric_hourly'));
    }

    public function testFaultsAreLimitsActuallyHitAndWarningsAreThresholds(): void
    {
        $sites = [['domain' => 'shop.test', 'account' => 'acct1', 'php' => self::php(1, 1 << 20, 0)]];
        $a = self::slice();
        $this->answer(0, $sites, [$a]);
        $this->svc()->collect(self::T0);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM site_resource_events'), 'a quiet account records nothing');

        // 60 s later: the kernel throttled CPU, the memory limit was reached twice, a process was killed; memory is at 95% of its limit.
        $a['usage'] = ['cpu_usage_usec' => 61_000_000, 'cpu_throttled_usec' => 2_000_000, 'cpu_nr_throttled' => 15, 'memory_max_events' => 2, 'oom_kills' => 1, 'memory_bytes' => 490 << 20, 'tasks' => 10] + $a['usage'];
        $this->answer(60, $sites, [$a]);
        $this->svc()->collect(self::T0 + 60);
        $ev = $this->db->fetchAll('SELECT kind, severity, occurrences FROM site_resource_events ORDER BY kind');
        $by = array_column($ev, 'severity', 'kind');
        self::assertSame('fault', $by['cpu_throttled']);
        self::assertSame('fault', $by['memory_limit']);
        self::assertSame('fault', $by['oom_kill']);
        self::assertSame('warning', $by['memory_near_limit'], 'crossing 90% is a warning, nothing was cut off');
        self::assertSame('warning', $by['cpu_near_limit'] ?? 'warning');

        // The same fault again within 15 minutes is the same event.
        $a['usage'] = ['cpu_usage_usec' => 62_000_000, 'cpu_nr_throttled' => 20] + $a['usage'];
        $this->answer(60, $sites, [$a]);
        $this->svc()->collect(self::T0 + 120);
        self::assertSame(2, (int) $this->db->scalar("SELECT occurrences FROM site_resource_events WHERE kind = 'cpu_throttled'"));

        // A counter that went backwards (the slice was recreated) yields no deltas and no fault.
        $before = (int) $this->db->scalar('SELECT COUNT(*) FROM site_resource_events');
        $a['usage'] = ['cpu_usage_usec' => 1000, 'cpu_nr_throttled' => 1, 'memory_bytes' => 10 << 20] + $a['usage'];
        $this->answer(60, $sites, [$a]);
        $this->svc()->collect(self::T0 + 180);
        self::assertSame($before, (int) $this->db->scalar('SELECT COUNT(*) FROM site_resource_events'));
        $last = $this->db->fetch('SELECT * FROM account_metric_samples ORDER BY id DESC LIMIT 1');
        self::assertNull($last['nr_throttled']);

        // An account without a slice (no limits) records nothing.
        $none = ['account' => 'acct9', 'users' => [], 'limits' => [], 'usage' => ['active' => false]];
        $this->answer(60, $sites, [$none]);
        $this->svc()->collect(self::T0 + 240);
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM account_metric_samples WHERE account = 'acct9'"));
    }

    public function testTheRankingSortsFiltersAndIsScopedToTheOwner(): void
    {
        foreach ([0, 60] as $dt) {
            $this->answer($dt === 0 ? 0 : 60, [
                ['domain' => 'shop.test', 'account' => 'acct1', 'php' => self::php(2, 300 << 20, $dt === 0 ? 0 : 30.0, 0, 5000), 'web' => $dt === 0 ? null : self::web(100, 5, 3, 900000)],
                ['domain' => 'blog.test', 'account' => 'acct2', 'php' => self::php(1, 50 << 20, $dt === 0 ? 0 : 3.0), 'web' => $dt === 0 ? null : self::web(1000, 0, 0, 100)],
            ]);
            $this->svc()->collect(self::T0 + $dt);
        }
        $admin = ['id' => $this->adminId, 'role_scope' => 'all'];
        $r = $this->svc()->ranking($admin, '24h', 'cpu', [], 100, self::T0 + 3600);
        self::assertSame(['shop.test', 'blog.test'], array_column($r, 'domain'));
        self::assertEqualsWithDelta(50.0, (float) $r[0]['cpu'], 0.1);
        self::assertSame(['blog.test', 'shop.test'], array_column($this->svc()->ranking($admin, '24h', 'requests', [], 100, self::T0 + 3600), 'domain'));
        self::assertSame(['shop.test', 'blog.test'], array_column($this->svc()->ranking($admin, '24h', 'errors', [], 100, self::T0 + 3600), 'domain'));
        self::assertSame(['blog.test'], array_column($this->svc()->ranking($admin, '24h', 'cpu', ['q' => 'blo'], 100, self::T0 + 3600), 'domain'));
        self::assertSame(['shop.test', 'blog.test'], array_column($this->svc()->ranking($admin, '1h', 'memory', [], 100, self::T0 + 600), 'domain'));
        self::assertSame([], $this->svc()->ranking($admin, '24h', 'cpu', ['faults' => true], 100, self::T0 + 3600), 'no faults were recorded');
        // The owner of one website sees only that website.
        $custId = (int) $this->db->scalar("SELECT id FROM users WHERE username = 'cust'");
        $mine = $this->svc()->ranking(['id' => $custId, 'role_scope' => 'own'], '24h', 'cpu', [], 100, self::T0 + 3600);
        self::assertSame(['shop.test'], array_column($mine, 'domain'));
        // An unknown sort key falls back to CPU instead of reaching the SQL.
        self::assertCount(2, $this->svc()->ranking($admin, '24h', "cpu; DROP TABLE websites", [], 100, self::T0 + 3600));
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    public function testPagesAreProtectedPerOwner(): void
    {
        $this->answer(60, [['domain' => 'shop.test', 'account' => 'acct1', 'php' => self::php(2, 100 << 20, 3.0), 'web' => self::web(10, 0, 0, 10)]]);
        $this->svc()->collect(time());
        $this->switchTo('cust');
        $page = $this->request('GET', '/sites/' . $this->shop . '/resources');
        self::assertSame(200, $page->status());
        foreach (['Resource usage', 'data-metric="site_cpu"', 'Not measured, and why', 'Faults and warnings', 'data-fixed-range="24h"'] as $want) {
            self::assertStringContainsString($want, $page->body(), $want);
        }
        self::assertSame(404, $this->request('GET', '/sites/' . $this->blog . '/resources')->status(), 'another customer\'s website');
        self::assertSame(404, $this->request('GET', '/sites/' . $this->blog . '/resources/series')->status());
        $series = json_decode($this->request('GET', '/sites/' . $this->shop . '/resources/series', [], [], '', ['range' => '1h'])->body(), true);
        self::assertTrue($series['success']);
        self::assertSame('1h', $series['range']);
        self::assertNotEmpty($series['points']);
        $rank = $this->request('GET', '/resources')->body();
        self::assertStringContainsString('shop.test', $rank);
        self::assertStringNotContainsString('blog.test', $rank, 'a customer sees only their own websites in the ranking');
    }

    public function testTheApiIsReadOnlyAndHonoursTheRange(): void
    {
        $this->answer(60, [['domain' => 'shop.test', 'account' => 'acct1', 'php' => self::php(2, 100 << 20, 3.0)]]);
        $this->svc()->collect(time());
        $token = $this->app->container->get(ApiTokenService::class)->create($this->adminId, ['name' => 'res', 'ability' => 'read', 'all_scopes' => '1', 'expires_days' => 30]);
        $call = fn (string $path): Response => $this->app->handle(new Request('GET', explode('?', $path)[0], (function () use ($path): array { parse_str(explode('?', $path)[1] ?? '', $q); return $q; })(), [], ['REMOTE_ADDR' => '192.0.2.50'], ['authorization' => 'Bearer ' . $token], ''));
        $res = $call('/api/v1/websites/' . $this->shop . '/resources?range=7d');
        self::assertSame(200, $res->status());
        $data = (array) json_decode($res->body(), true)['data'];
        self::assertSame('7d', $data['range']);
        self::assertSame('shop.test', $data['domain']);
        self::assertSame(404, $call('/api/v1/websites/99999/resources')->status());
        $rank = (array) json_decode($call('/api/v1/resources/ranking?sort=memory')->body(), true)['data'];
        self::assertSame('memory', $rank['sort']);
        self::assertNotEmpty($rank['websites']);
    }

    /** @param array<string, mixed> $o @return array<string, mixed> */
    private static function fpm(array $o = []): array
    {
        return $o + ['active' => 1, 'idle' => 2, 'total' => 3, 'queue' => 0, 'max_queue' => 0, 'max_active' => 2, 'start_time' => 1000, 'max_children_reached' => 0, 'slow_requests' => 0, 'accepted' => 30];
    }

    public function testPhpFpmStatusIsRecordedAndSaturationIsAFault(): void
    {
        $site = fn (array $fpm): array => [['domain' => 'shop.test', 'account' => '', 'php' => self::php(3, 90 << 20, 1.0) + ['fpm' => $fpm]]];
        // First sample: instants only, no deltas.
        $this->answer(0, $site(self::fpm(['max_children_reached' => null, 'slow_requests' => null, 'accepted' => null])));
        $this->svc()->collect(self::T0);
        $row = $this->db->fetch('SELECT * FROM site_metric_samples WHERE website_id = ?', [$this->shop]);
        self::assertSame([1, 2, 3, 0, 2], [(int) $row['fpm_active'], (int) $row['fpm_idle'], (int) $row['fpm_total'], (int) $row['fpm_queue'], (int) $row['fpm_max_active']]);
        self::assertNull($row['fpm_hits'], 'no delta on the first sample');
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM site_resource_events'));

        // The pool hit pm.max_children 3 times (limit 4 in the website's settings), 2 requests wait, all 4 workers are busy.
        $this->answer(60, $site(self::fpm(['active' => 4, 'idle' => 0, 'total' => 4, 'queue' => 2, 'max_active' => 4, 'max_children_reached' => 3, 'slow_requests' => 1, 'accepted' => 25])));
        $this->svc()->collect(self::T0 + 60);
        $by = array_column($this->db->fetchAll('SELECT kind, severity, website_id FROM site_resource_events'), 'severity', 'kind');
        self::assertSame('fault', $by['fpm_max_children'], 'the limit was actually hit');
        self::assertSame('warning', $by['fpm_queue']);
        self::assertArrayNotHasKey('fpm_near_limit', $by, 'a hit is already a fault, not also a "nearly" warning');
        self::assertSame($this->shop, (int) $this->db->scalar("SELECT website_id FROM site_resource_events WHERE kind = 'fpm_max_children'"), 'a pool fault belongs to its website, not to an account');
        $row = $this->db->fetch('SELECT * FROM site_metric_samples WHERE website_id = ? ORDER BY taken_at DESC LIMIT 1', [$this->shop]);
        self::assertSame([3, 1, 25], [(int) $row['fpm_hits'], (int) $row['fpm_slow'], (int) $row['fpm_accepted']]);

        // Busy but not yet at the limit: a warning only.
        $this->answer(60, $site(self::fpm(['active' => 4, 'idle' => 0, 'total' => 4, 'max_children_reached' => 0, 'slow_requests' => 0, 'accepted' => 15])));
        $this->svc()->collect(self::T0 + 7200);
        self::assertSame('warning', $this->db->scalar("SELECT severity FROM site_resource_events WHERE kind = 'fpm_near_limit'"));

        // The site page and the ranking see the website's own events; the series carries the new keys.
        $events = $this->svc()->events($this->shop, '24h', 50, self::T0 + 7300);
        self::assertContains('fpm_max_children', array_column($events, 'kind'));
        $admin = ['id' => $this->adminId, 'role_scope' => 'all'];
        $rank = $this->svc()->ranking($admin, '24h', 'faults', [], 100, self::T0 + 7300);
        self::assertSame('shop.test', $rank[0]['domain']);
        self::assertSame(1, $rank[0]['faults']);
        $pts = $this->svc()->series($this->shop, '24h', self::T0 + 7300);
        self::assertSame(4, max(array_column($pts, 'fpm_active_max')));
        self::assertSame(2, max(array_column($pts, 'fpm_queue')));
        $h = $this->db->fetch('SELECT fpm_hits, fpm_active_max FROM site_metric_hourly WHERE website_id = ? AND bucket = ?', [$this->shop, self::T0]);
        self::assertSame([3, 4], [(int) $h['fpm_hits'], (int) $h['fpm_active_max']]);
    }

    public function testASiteWithoutStatusPageHasNullFpmColumns(): void
    {
        $this->answer(60, [['domain' => 'shop.test', 'php' => self::php(3, 90 << 20, 1.0)]]);
        $this->svc()->collect(self::T0);
        $row = $this->db->fetch('SELECT fpm_active, fpm_queue, fpm_hits FROM site_metric_samples');
        self::assertSame([null, null, null], array_values($row));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM site_resource_events'));
        $pts = $this->svc()->series($this->shop, '1h', self::T0 + 100);
        self::assertNull($pts[0]['fpm_active'], 'a gap, not zero');
    }

    public function testOnlyAdministratorsSeeAndUseTheSwitch(): void
    {
        $this->agent->succeed('sites.fpm_status', ['enabled' => false, 'php_sites' => 2, 'with_status' => 0]);
        $this->agent->succeed('sites.fpm_status_set', ['enabled' => true, 'php_sites' => 2, 'with_status' => 1, 'skipped' => ['hand.test (pool file was changed by hand)']]);
        $this->login('admin');
        $page = $this->request('GET', '/resources')->body();
        foreach (['PHP-FPM status', 'Switch on', 'name="enabled" value="1"', 'data-nim-confirm=', 'Off: the pools publish nothing'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $this->post('/resources/fpm-status', ['enabled' => '1']);
        self::assertTrue($this->lastCall('sites.fpm_status_set')['enabled']);
        $flash = (string) $this->flash('success');
        self::assertStringContainsString('1 of 2 PHP websites', $flash);
        self::assertStringContainsString('hand.test', $flash, 'pools left alone are named');
        self::assertSame('success', $this->db->scalar("SELECT result FROM audit_logs WHERE action = 'resources.fpm_status'"));

        $this->switchTo('cust');
        self::assertStringNotContainsString('PHP-FPM status', $this->request('GET', '/resources')->body(), 'customers do not see the switch');
        $calls = $this->calls('sites.fpm_status_set');
        self::assertContains($this->post('/resources/fpm-status', ['enabled' => '1'])->status(), [302, 403]);
        self::assertSame($calls, $this->calls('sites.fpm_status_set'), 'a customer cannot change it');
    }

    public function testAnOldAgentHidesTheSwitch(): void
    {
        $this->agent->fail('sites.fpm_status', 400, 'unknown_action', 'Unknown action');
        $this->login('admin');
        self::assertStringNotContainsString('Switch on', $this->request('GET', '/resources')->body());
    }
}
