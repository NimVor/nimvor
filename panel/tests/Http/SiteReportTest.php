<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Services\GeoIpService;
use NimVor\Tests\Support\HttpTestCase;

/** A website's traffic report. */
final class SiteReportTest extends HttpTestCase
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
        $now = time();
        $this->agent->succeed('webreport.site', [
            'exists' => true, 'truncated' => false, 'hours' => 24, 'requests' => 1200, 'refused' => 340, 'limited' => 300, 'closed' => 10, 'forbidden' => 30,
            'classes' => [0, 0, 800, 50, 340, 10],
            'timeline' => [['start' => $now - 7200, 'requests' => 100, 'refused' => 20], ['start' => $now - 3600, 'requests' => 500, 'refused' => 300], ['start' => $now, 'requests' => 600, 'refused' => 20]],
            'ips' => [['key' => '203.0.113.9', 'total' => 400, 'refused' => 300], ['key' => '198.51.100.4', 'total' => 90, 'refused' => 0]],
            'refused_ips' => [['key' => '203.0.113.9', 'total' => 400, 'refused' => 300]],
            'urls' => [['key' => '/wp-login.php', 'total' => 350, 'refused' => 300], ['key' => '/', 'total' => 200, 'refused' => 0]],
            'refused_urls' => [['key' => '/wp-login.php', 'total' => 350, 'refused' => 300]],
            'refused_agents' => [['key' => 'scanner/1.0', 'total' => 310, 'refused' => 310]],
            'addresses' => [['key' => '203.0.113.9', 'total' => 400, 'refused' => 300], ['key' => '198.51.100.4', 'total' => 90, 'refused' => 0], ['key' => '192.0.2.1', 'total' => 5, 'refused' => 0]],
        ]);
        $this->agent->succeed('modsec.events', ['events' => [
            ['client_ip' => '203.0.113.9', 'uri' => '/wp-login.php?a=1', 'rules' => [['id' => '942100', 'message' => 'SQL Injection']]],
            ['client_ip' => '203.0.113.9', 'uri' => '/x', 'rules' => [['id' => '942100', 'message' => 'SQL Injection'], ['id' => '941100', 'message' => 'XSS']]],
        ]]);
    }

    private function loadDatabase(): void
    {
        $csv = sys_get_temp_dir() . '/nimvor-geo-' . bin2hex(random_bytes(4)) . '.csv';
        file_put_contents($csv, "198.51.100.0,198.51.100.255,DE\n203.0.113.0,203.0.113.255,KP\n");
        $this->app->container->get(GeoIpService::class)->import($csv);
        @unlink($csv);
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    public function testTheReportShowsTheNumbers(): void
    {
        $this->loadDatabase();
        $this->login('admin');
        $site = '/sites/' . $this->siteId;
        self::assertStringContainsString('href="' . $site . '/report"', $this->request('GET', $site)->body(), 'the tile links to the report');
        $page = $this->request('GET', $site . '/report')->body();
        foreach (['1,200', '340', 'Traffic limit (429)', '203.0.113.9', '/wp-login.php', 'scanner/1.0', 'Korea', 'Germany', 'Unknown or private', 'SQL Injection', '942100', 'ModSecurity', 'Last 7 days', 'Requests per hour', 'name="value" value="203.0.113.9"'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertStringNotContainsString('?a=1', $page, 'query strings are never shown');
        self::assertStringNotContainsString('<script>', $page, 'no inline scripts');
        $call = $this->lastCall('webreport.site');
        self::assertSame(['domain' => 'shop.test', 'hours' => 24], $call);
        // ModSecurity events: the website's names only.
        self::assertContains('shop.test', $this->lastCall('modsec.events')['hosts']);
    }

    public function testThePeriodIsChosenFromAFixedList(): void
    {
        $this->login('admin');
        $this->request('GET', '/sites/' . $this->siteId . '/report', [], [], '', ['hours' => '168']);
        self::assertSame(168, $this->lastCall('webreport.site')['hours']);
        $this->request('GET', '/sites/' . $this->siteId . '/report', [], [], '', ['hours' => '999999']);
        self::assertSame(24, $this->lastCall('webreport.site')['hours']);
        $this->request('GET', '/sites/' . $this->siteId . '/report', [], [], '', ['hours' => 'abc']);
        self::assertSame(24, $this->lastCall('webreport.site')['hours']);
    }

    public function testWithoutALogOrADatabase(): void
    {
        $this->login('admin');
        $this->agent->succeed('webreport.site', ['exists' => false]);
        self::assertStringContainsString('No access log yet', $this->request('GET', '/sites/' . $this->siteId . '/report')->body());
        $this->agent->succeed('webreport.site', ['exists' => true, 'requests' => 3, 'timeline' => [], 'addresses' => [['key' => '203.0.113.9', 'total' => 3, 'refused' => 0]]]);
        $page = $this->request('GET', '/sites/' . $this->siteId . '/report')->body();
        self::assertStringContainsString('Countries need the country database', $page);
    }

    public function testOwnersSeeTheirOwnSiteOnly(): void
    {
        $this->switchTo('cust');
        self::assertSame(200, $this->request('GET', '/sites/' . $this->siteId . '/report')->status());
        $before = $this->calls('webreport.site');
        self::assertSame(404, $this->request('GET', '/sites/' . $this->otherSite . '/report')->status());
        self::assertSame($before, $this->calls('webreport.site'), "another customer's log is never read");
    }
}
