<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Repositories\UserRepository;
use WhaCP\Services\StatsService;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/** Website statistics (AWStats): turning on, the report, who may see it. */
final class StatsTest extends HttpTestCase
{
    private int $siteId;

    private const DATA = <<<'TXT'
AWSTATS DATA FILE 7.9 (build 20230108)
# comment
BEGIN_GENERAL 8
LastLine 20260927150000 1234 0 0
FirstTime 20260901000512
LastTime 20260927145959
LastUpdate 20260927150102 1234 0 1200 30 4
TotalVisits 120
TotalUnique 80
MonthHostsKnown 0
MonthHostsUnknown 80
END_GENERAL
BEGIN_DAY 2
20260901 50 200 1048576 40
20260902 70 300 2097152 80
END_DAY
BEGIN_TIME 2
9 30 100 500000 0 0 0
14 90 400 2645728 0 0 0
END_TIME
BEGIN_SIDER 2
/ 80 1000 60 50
/shop/<script>alert(1)</script> 40 500 10 20
END_SIDER
BEGIN_BROWSER 3
chrome120 150
chrome119 50
firefox121 100
END_BROWSER
BEGIN_OS 2
win10 180
linuxubuntu 60
END_OS
BEGIN_DOMAIN 2
pk 60 300 1000
ip 10 50 200
END_DOMAIN
BEGIN_ERRORS 1
404 12 3000
END_ERRORS
TXT;

    protected function setUp(): void
    {
        parent::setUp();
        $cust = $this->seedUser('cust', 'customer');
        $this->seedUser('mallory', 'customer');
        $this->seedUser('admin', 'admin');
        $this->siteId = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $this->app->container->get(UserRepository::class)->find($cust));
    }

    public function testParsesAwstatsData(): void
    {
        $r = StatsService::parse(self::DATA);
        self::assertSame([80, 120, 120, 500, 3145728], [$r['unique'], $r['visits'], $r['pages'], $r['hits'], $r['bytes']]);
        self::assertSame('2026-09-27T15:01:02Z', $r['updated']);
        self::assertSame(['pages' => 90, 'hits' => 400, 'bytes' => 2645728], $r['hours'][14]);
        self::assertSame([['label' => 'Chrome', 'value' => 200], ['label' => 'Firefox', 'value' => 100]], $r['browsers'], 'versions merged');
        self::assertSame('Windows', $r['systems'][0]['label']);
        self::assertSame([['label' => 'PK', 'value' => 300], ['label' => 'Unknown', 'value' => 50]], $r['countries']);
        self::assertSame('404', $r['errors'][0]['label']);
        self::assertSame([], StatsService::parse("garbage\nBEGIN_DAY 1\nnot-a-date 1 2 3 4\nEND_DAY\n")['days']);
    }

    /** Output of AWStats 7.9 itself (NimVor's configuration, a combined-format log). */
    public function testParsesRealAwstatsOutput(): void
    {
        $r = StatsService::parse((string) file_get_contents(dirname(__DIR__) . '/Fixtures/awstats-7.9-real.txt'));
        self::assertSame([40, 211, 5630308], [$r['unique'], $r['pages'], $r['bytes']]);
        self::assertCount(3, $r['days']);
        self::assertSame(['/about', '/', '/shop'], array_column($r['pages_top'], 'label'));
        self::assertSame(['Firefox', 'Chrome', 'Safari'], array_column($r['browsers'], 'label'));
        self::assertSame(['Linux', 'Windows', 'iOS'], array_column($r['systems'], 'label'));
        self::assertSame([['label' => 'google.com', 'value' => 72]], $r['search']);
        self::assertSame([['label' => '404', 'value' => 89]], $r['errors']);
    }

    public function testTurnOnAndReport(): void
    {
        $base = '/sites/' . $this->siteId . '/stats';
        $this->agent->succeed('stats.status', ['installed' => false, 'enabled' => false, 'months' => []]);
        $this->login('cust');
        self::assertStringContainsString('ask your hosting provider', $this->request('GET', $base)->body());

        $this->agent->succeed('stats.status', ['installed' => true, 'enabled' => false, 'months' => []]);
        self::assertStringContainsString('Turn on statistics', $this->request('GET', $base)->body());
        $this->agent->succeed('stats.enable', ['enabled' => true]);
        $this->agent->succeed('stats.update', ['job_id' => 'eeeeeeeeeeeeeeee']);
        $this->post($base . '/enable', ['enabled' => '1']);
        $call = $this->lastCall('stats.enable');
        self::assertSame(['shop.test', true, '/home/whacp/shop.test'], [$call['domain'], $call['enabled'], $call['root']]);
        self::assertSame($call['user'], $this->lastCall('stats.update')['user'], 'updated as the website account');

        $this->agent->succeed('stats.status', ['installed' => true, 'enabled' => true, 'months' => ['202609', '202608'], 'updated_at' => gmdate('c')]);
        $this->agent->succeed('stats.data', ['found' => true, 'content' => self::DATA]);
        $page = $this->request('GET', $base)->body();
        foreach (['September 2026', 'Unique visitors', '3 MB', 'Chrome', 'Most viewed pages', 'Update now'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertStringContainsString('/shop/&lt;script&gt;alert(1)&lt;/script&gt;', $page, 'data is text, escaped');
        self::assertStringNotContainsString('<script>alert(1)', $page);
        self::assertSame('202609', $this->lastCall('stats.data')['month']);
        $this->request('GET', $base, [], [], '', ['month' => '../../etc']);
        self::assertSame('202609', $this->lastCall('stats.data')['month'], 'only months the agent listed');

        $this->session = new \WhaCP\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login('mallory');
        self::assertSame(404, $this->request('GET', $base)->status());
        $before = $this->calls('stats.enable');
        $this->post($base . '/enable', ['enabled' => '0']);
        self::assertSame($before, $this->calls('stats.enable'));
    }

    public function testDeletingAWebsiteTurnsItsStatisticsOff(): void
    {
        $this->agent->succeed('stats.enable', ['enabled' => false]);
        $this->agent->succeed('sites.delete', ['warnings' => []]);
        $this->login('admin');
        $this->post('/sites/' . $this->siteId . '/delete', ['confirm_domain' => 'shop.test']);
        self::assertFalse($this->lastCall('stats.enable')['enabled']);
    }
}
