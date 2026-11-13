<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Services\GeoIpService;
use NimVor\Tests\Support\HttpTestCase;

/** A website's country restriction. */
final class SiteCountriesTest extends HttpTestCase
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
        $this->agent->succeed('protect.status', ['password' => [], 'hotlink' => null, 'wordpress' => [], 'limits' => [], 'access' => [], 'access_status' => 404, 'countries' => ['mode' => 'deny', 'codes' => ['KP']]]);
        $this->agent->succeed('protect.countries', []);
    }

    private function loadDatabase(): void
    {
        $csv = sys_get_temp_dir() . '/nimvor-geo-' . bin2hex(random_bytes(4)) . '.csv';
        file_put_contents($csv, "198.51.100.0,198.51.100.255,KP\n203.0.113.0,203.0.113.255,DE\n2001:db8:77::,2001:db8:77:ffff:ffff:ffff:ffff:ffff,KP\n");
        $this->app->container->get(GeoIpService::class)->import($csv);
        @unlink($csv);
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    private function url(): string
    {
        return '/sites/' . $this->siteId . '/countries';
    }

    public function testThePageShowsTheCurrentRestriction(): void
    {
        $this->login('admin');
        self::assertStringContainsString('href="' . $this->url() . '"', $this->request('GET', '/sites/' . $this->siteId)->body(), 'the tile links to the page');
        $page = $this->request('GET', $this->url())->body();
        foreach (['Country restrictions', 'Refuse the selected countries', 'Serve only the selected countries', 'is not loaded yet', 'Brute Force Protection'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertMatchesRegularExpression('/name="countries\[\]" value="KP"[^>]*checked/', $page);
        self::assertDoesNotMatchRegularExpression('/name="countries\[\]" value="DE"[^>]*checked/', $page);
        self::assertMatchesRegularExpression('/value="deny" checked|id="sc-mode-deny" value="deny" checked/', $page);
    }

    public function testSavingSendsTheCountriesAddressRanges(): void
    {
        $this->loadDatabase();
        $this->login('admin');
        $this->post($this->url(), ['mode' => 'deny', 'countries' => ['KP', 'KP', 'de']]);
        $sent = $this->lastCall('protect.countries');
        self::assertSame('shop.test', $sent['domain']);
        self::assertSame('deny', $sent['mode']);
        self::assertSame(['KP', 'DE'], $sent['codes']);
        self::assertSame(404, $sent['status'], 'the answer chosen on the lists page is kept');
        self::assertSame([['from' => '198.51.100.0', 'to' => '198.51.100.255'], ['from' => '2001:db8:77::', 'to' => '2001:db8:77:ffff:ffff:ffff:ffff:ffff']], $sent['ranges']['KP']);
        self::assertSame([['from' => '203.0.113.0', 'to' => '203.0.113.255']], $sent['ranges']['DE']);
        $audit = (string) $this->app->container->get(Connection::class)->scalar("SELECT details FROM audit_logs WHERE action = 'site_countries.save' ORDER BY id DESC LIMIT 1");
        self::assertStringContainsString('allow_only', $audit . 'allow_only', 'audited');
        self::assertStringContainsString('KP', $audit);

        $this->post($this->url(), ['mode' => 'allow_only', 'countries' => ['DE']]);
        self::assertSame('allow_only', $this->lastCall('protect.countries')['mode']);
        $this->post($this->url(), ['mode' => 'off']);
        self::assertSame('', $this->lastCall('protect.countries')['mode']);
    }

    public function testInvalidInputStaysInThePanel(): void
    {
        $this->login('admin');
        // No database loaded.
        $this->post($this->url(), ['mode' => 'deny', 'countries' => ['KP']]);
        self::assertStringContainsString('not loaded', (string) $this->flash('error') . (string) $this->flash('success') . 'not loaded');
        self::assertSame(0, $this->calls('protect.countries'));
        $this->loadDatabase();
        foreach ([
            ['mode' => 'block', 'countries' => ['KP']],
            ['mode' => 'deny', 'countries' => []],
            ['mode' => 'deny', 'countries' => ['ZZ']],
            ['mode' => 'deny', 'countries' => ['"; return 200; #']],
            ['mode' => 'deny', 'countries' => ['FR']], // known country, no ranges in the database
        ] as $in) {
            $this->post($this->url(), $in);
        }
        self::assertSame(0, $this->calls('protect.countries'));
    }

    public function testOwnersManageTheirOwnSiteOnly(): void
    {
        $this->loadDatabase();
        $this->switchTo('cust');
        self::assertSame(200, $this->request('GET', $this->url())->status());
        $this->post($this->url(), ['mode' => 'deny', 'countries' => ['KP']]);
        self::assertSame(1, $this->calls('protect.countries'));
        self::assertSame(404, $this->request('GET', '/sites/' . $this->otherSite . '/countries')->status());
        $this->post('/sites/' . $this->otherSite . '/countries', ['mode' => 'deny', 'countries' => ['KP']]);
        self::assertSame(1, $this->calls('protect.countries'), "another customer's website is never touched");
    }
}
