<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Tests\Support\HttpTestCase;

/** A website's Site access page: HTTP basic authentication for the site or a folder. */
final class SiteAccessTest extends HttpTestCase
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
        $this->agent->succeed('protect.status', ['password' => [['path' => '/admin/', 'user' => 'editor', 'realm' => 'Restricted area']], 'hotlink' => null, 'wordpress' => []]);
        $this->agent->succeed('protect.password', ['password' => []]);
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    public function testTheTileOpensThePageAndItListsProtectedAddresses(): void
    {
        $this->login('admin');
        $overview = $this->request('GET', '/sites/' . $this->siteId)->body();
        self::assertStringContainsString('href="/sites/' . $this->siteId . '/access"', $overview, 'the Site access tile links to the page');
        $page = $this->request('GET', '/sites/' . $this->siteId . '/access')->body();
        foreach (['Site access', 'shop.test/admin/', 'editor', 'name="path"', 'name="user"', 'name="password"', 'type="password"'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
    }

    public function testProtectingSendsTheRuleAndNeverShowsThePassword(): void
    {
        $this->login('admin');
        $this->post('/sites/' . $this->siteId . '/access', ['path' => '/', 'user' => 'visitor', 'password' => 'correct-horse-9', 'realm' => '']);
        $sent = $this->lastCall('protect.password');
        self::assertSame(['domain' => 'shop.test', 'path' => '/', 'enabled' => true, 'user' => 'visitor', 'password' => 'correct-horse-9', 'realm' => 'Restricted area'], $sent);
        self::assertStringContainsString('must sign in as visitor', (string) $this->flash('success'));
        self::assertStringNotContainsString('correct-horse-9', $this->request('GET', '/sites/' . $this->siteId . '/access')->body());
        $audit = (string) $this->app->container->get(Connection::class)->scalar("SELECT details FROM audit_logs WHERE action = 'site_access.protect' ORDER BY id DESC LIMIT 1");
        self::assertStringNotContainsString('correct-horse-9', $audit);

        // A folder, with the leading and trailing slashes optional.
        $this->post('/sites/' . $this->siteId . '/access', ['path' => 'private/files', 'user' => 'a.b-c_d', 'password' => 'another-pass-1']);
        self::assertSame('/private/files/', $this->lastCall('protect.password')['path']);
    }

    public function testInvalidInputStaysInThePanel(): void
    {
        $this->login('admin');
        foreach ([
            ['path' => '/', 'user' => 'bad user', 'password' => 'long-enough-1'],
            ['path' => '/', 'user' => 'ok', 'password' => 'short'],
            ['path' => '/../etc/', 'user' => 'ok', 'password' => 'long-enough-1'],
            ['path' => '/a b/', 'user' => 'ok', 'password' => 'long-enough-1'],
            ['path' => '/', 'user' => 'ok', 'password' => 'long-enough-1', 'realm' => 'quote " here'],
        ] as $input) {
            $this->post('/sites/' . $this->siteId . '/access', $input);
        }
        self::assertSame(0, $this->calls('protect.password'));
    }

    public function testTurningItOff(): void
    {
        $this->login('admin');
        $this->post('/sites/' . $this->siteId . '/access/remove', ['path' => '/admin/']);
        self::assertSame(['domain' => 'shop.test', 'path' => '/admin/', 'enabled' => false], $this->lastCall('protect.password'));
        self::assertStringContainsString('is off', (string) $this->flash('success'));
    }

    public function testOwnersManageTheirOwnSiteOnly(): void
    {
        $this->switchTo('cust');
        self::assertSame(200, $this->request('GET', '/sites/' . $this->siteId . '/access')->status());
        $this->post('/sites/' . $this->siteId . '/access', ['path' => '/', 'user' => 'me', 'password' => 'long-enough-1']);
        self::assertSame(1, $this->calls('protect.password'));
        self::assertSame(404, $this->request('GET', '/sites/' . $this->otherSite . '/access')->status());
        $this->post('/sites/' . $this->otherSite . '/access', ['path' => '/', 'user' => 'me', 'password' => 'long-enough-1']);
        $this->post('/sites/' . $this->otherSite . '/access/remove', ['path' => '/']);
        self::assertSame(1, $this->calls('protect.password'), "another customer's website is never touched");
    }
}
