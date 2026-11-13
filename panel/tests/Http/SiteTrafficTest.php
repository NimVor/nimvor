<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Tests\Support\HttpTestCase;

/** A website's Traffic protection page: traffic limits. */
final class SiteTrafficTest extends HttpTestCase
{
    private int $siteId;
    private int $otherSite;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $other = $this->seedUser('other', 'customer');
        $db = $this->app->container->get(Connection::class);
        foreach ([['shop.test', $cust], ['blog.test', $other]] as [$d, $o]) {
            $db->run("INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, ?, 'static', ?, ?, ?, ?, 'active', 'x', 'x')", [$d, '/home/u/' . $d, '/home/u/' . $d . '/public_html', 'u' . $o, $o]);
        }
        $this->siteId = (int) $db->scalar("SELECT id FROM websites WHERE domain = 'shop.test'");
        $this->otherSite = (int) $db->scalar("SELECT id FROM websites WHERE domain = 'blog.test'");
        $this->agent->succeed('protect.status', ['password' => [], 'hotlink' => null, 'wordpress' => [], 'limit_status' => 503, 'limits' => [
            ['name' => 'login', 'match' => 'exact', 'path' => '/wp-login.php', 'rate' => 5, 'unit' => 'm', 'burst' => 3, 'per_ua' => false, 'enabled' => true],
            ['name' => 'api', 'match' => 'prefix', 'path' => '/api/', 'rate' => 20, 'unit' => 's', 'burst' => 0, 'per_ua' => true, 'enabled' => false],
        ]]);
        $this->agent->succeed('protect.limits', []);
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    private function url(string $suffix = ''): string
    {
        return '/sites/' . $this->siteId . '/traffic' . $suffix;
    }

    public function testTheTileOpensThePageAndItListsTheLimits(): void
    {
        $this->login('admin');
        self::assertStringContainsString('href="' . $this->url() . '"', $this->request('GET', '/sites/' . $this->siteId)->body(), 'the Traffic protection tile links to the page');
        $page = $this->request('GET', $this->url())->body();
        foreach (['Traffic limits', 'shop.test/wp-login.php', 'exact', '5 per minute, burst 3', 'per address and browser', '20 per second', 'Turn on', 'Turn off', 'value="503" selected', 'name="rate"', 'name="per_ua"', 'every visitor has the proxy'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
    }

    public function testSavingALimitSendsTheWholeListWithTheNewRule(): void
    {
        $this->login('admin');
        $this->post($this->url('/limit'), ['name' => 'Search', 'match' => 'prefix', 'path' => 'search/', 'rate' => '2', 'unit' => 's', 'burst' => '5', 'per_ua' => '1']);
        $sent = $this->lastCall('protect.limits');
        self::assertSame('shop.test', $sent['domain']);
        self::assertSame(503, $sent['status']);
        self::assertCount(3, $sent['limits']);
        self::assertSame(['name' => 'search', 'match' => 'prefix', 'path' => '/search/', 'rate' => 2, 'unit' => 's', 'burst' => 5, 'per_ua' => true, 'enabled' => true], $sent['limits'][2]);
        self::assertStringContainsString('is on', (string) $this->flash('success'));

        // The same name changes the rule instead of adding one.
        $this->post($this->url('/limit'), ['name' => 'login', 'match' => 'exact', 'path' => '/wp-login.php', 'rate' => '10', 'unit' => 'm', 'burst' => '0']);
        $sent = $this->lastCall('protect.limits');
        self::assertCount(2, $sent['limits']);
        self::assertSame(10, $sent['limits'][0]['rate']);
        $audit = (string) $this->app->container->get(Connection::class)->scalar("SELECT details FROM audit_logs WHERE action = 'site_traffic.limit_save' ORDER BY id DESC LIMIT 1");
        self::assertStringContainsString('wp-login', $audit);
    }

    public function testInvalidInputStaysInThePanel(): void
    {
        $this->login('admin');
        foreach ([
            ['name' => 'Bad Name', 'match' => 'prefix', 'path' => '/', 'rate' => '5', 'unit' => 's'],
            ['name' => 'x', 'match' => 'regex', 'path' => '/', 'rate' => '5', 'unit' => 's'],
            ['name' => 'x', 'match' => 'prefix', 'path' => '/a b', 'rate' => '5', 'unit' => 's'],
            ['name' => 'x', 'match' => 'prefix', 'path' => '/../etc', 'rate' => '5', 'unit' => 's'],
            ['name' => 'x', 'match' => 'prefix', 'path' => '/x";}', 'rate' => '5', 'unit' => 's'],
            ['name' => 'x', 'match' => 'prefix', 'path' => '/', 'rate' => '0', 'unit' => 's'],
            ['name' => 'x', 'match' => 'prefix', 'path' => '/', 'rate' => '5000', 'unit' => 's'],
            ['name' => 'x', 'match' => 'prefix', 'path' => '/', 'rate' => '5', 'unit' => 'h'],
            ['name' => 'x', 'match' => 'prefix', 'path' => '/', 'rate' => '5', 'unit' => 's', 'burst' => '5000'],
        ] as $input) {
            $this->post($this->url('/limit'), $input);
        }
        $this->post($this->url('/status'), ['status' => '200']);
        self::assertSame(0, $this->calls('protect.limits'));
    }

    public function testTogglingRemovingAndTheAnswer(): void
    {
        $this->login('admin');
        $this->post($this->url('/limit/toggle'), ['name' => 'api', 'enabled' => '1']);
        $sent = $this->lastCall('protect.limits');
        self::assertTrue($sent['limits'][1]['enabled']);
        $this->post($this->url('/limit/remove'), ['name' => 'login']);
        self::assertSame(['api'], array_column($this->lastCall('protect.limits')['limits'], 'name'));
        $this->post($this->url('/status'), ['status' => '444']);
        self::assertSame(444, $this->lastCall('protect.limits')['status']);
        $before = $this->calls('protect.limits');
        $this->post($this->url('/limit/remove'), ['name' => 'nothing']);
        $this->post($this->url('/limit/toggle'), ['name' => 'nothing', 'enabled' => '1']);
        self::assertSame($before, $this->calls('protect.limits'), 'a rule that does not exist is not sent');
    }

    public function testOwnersManageTheirOwnSiteOnly(): void
    {
        $this->switchTo('cust');
        self::assertSame(200, $this->request('GET', $this->url())->status());
        $this->post($this->url('/limit'), ['name' => 'me', 'match' => 'prefix', 'path' => '/', 'rate' => '5', 'unit' => 's']);
        self::assertSame(1, $this->calls('protect.limits'));
        self::assertSame(404, $this->request('GET', '/sites/' . $this->otherSite . '/traffic')->status());
        $this->post('/sites/' . $this->otherSite . '/traffic/limit', ['name' => 'me', 'match' => 'prefix', 'path' => '/', 'rate' => '5', 'unit' => 's']);
        $this->post('/sites/' . $this->otherSite . '/traffic/limit/remove', ['name' => 'login']);
        self::assertSame(1, $this->calls('protect.limits'), "another customer's website is never touched");
    }
}
