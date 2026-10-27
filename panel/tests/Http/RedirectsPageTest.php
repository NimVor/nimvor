<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Tests\Support\HttpTestCase;

/** A website's Redirects page (one record at a time) and PHP settings page. */
final class RedirectsPageTest extends HttpTestCase
{
    private int $siteId;
    private int $otherSite;
    private int $staticSite;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $other = $this->seedUser('other', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        foreach ([['shop.test', $cust, 'php'], ['blog.test', $other, 'php'], ['flat.test', $cust, 'static']] as [$d, $o, $kind]) {
            $this->db->run("INSERT INTO websites (server_id, domain, kind, php_version, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, ?, ?, ?, ?, ?, ?, ?, 'active', 'x', 'x')", [$d, $kind, $kind === 'php' ? '8.3' : '', '/home/u/' . $d, '/home/u/' . $d . '/public_html', 'u' . $o . $kind, $o]);
        }
        $this->siteId = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'shop.test'");
        $this->otherSite = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'blog.test'");
        $this->staticSite = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'flat.test'");
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->agent->succeed('php.versions', ['versions' => []]);
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    private function stored(int $id): array
    {
        return (array) json_decode((string) $this->db->scalar('SELECT redirects FROM websites WHERE id = ?', [$id]), true);
    }

    private function url(string $tail = ''): string
    {
        return '/sites/' . $this->siteId . '/redirects' . $tail;
    }

    public function testTheTileOpensAPageWithTheRecordsAndAnAddButton(): void
    {
        $this->login('cust');
        $overview = $this->request('GET', '/sites/' . $this->siteId)->body();
        self::assertStringContainsString('href="' . $this->url() . '"', $overview, 'a Redirects tile');
        $page = $this->request('GET', $this->url())->body();
        foreach (['Redirects', 'No redirects yet', 'Add redirect', 'id="new-redirect"', 'name="from"', 'name="to"', 'name="code"', 'Permanent (301)', 'Temporary (302)', 'http://shop.test'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertStringNotContainsString('<textarea', $page, 'no text area of rules');
    }

    public function testAddingRedirectsOneByOne(): void
    {
        $this->login('cust');
        $this->post($this->url(), ['code' => '301', 'from' => '/old-page', 'to' => 'https://example.com/new-page']);
        self::assertStringContainsString('now redirects', (string) $this->flash('success'));
        self::assertSame([['from' => '/old-page', 'to' => 'https://example.com/new-page', 'code' => 301]], $this->stored($this->siteId));
        self::assertSame([['from' => '/old-page', 'to' => 'https://example.com/new-page', 'code' => 301]], $this->lastCall('sites.apply')['redirects']);

        // A second one is added to the first; the leading slash is optional.
        $this->post($this->url(), ['code' => '302', 'from' => 'blog/2019/post', 'to' => '/news']);
        self::assertSame([
            ['from' => '/old-page', 'to' => 'https://example.com/new-page', 'code' => 301],
            ['from' => '/blog/2019/post', 'to' => '/news', 'code' => 302],
        ], $this->stored($this->siteId));
        $page = $this->request('GET', $this->url())->body();
        foreach (['shop.test/old-page', 'https://example.com/new-page', 'shop.test/blog/2019/post', 'Permanent', 'Temporary', 'Delete'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertStringContainsString('2 of 100 redirects', $page);
    }

    public function testDeletingOneRedirectKeepsTheOthers(): void
    {
        $this->login('cust');
        $this->post($this->url(), ['code' => '301', 'from' => '/a', 'to' => '/b']);
        $this->post($this->url(), ['code' => '301', 'from' => '/c', 'to' => '/d']);
        $this->post($this->url('/delete'), ['from' => '/a']);
        self::assertSame([['from' => '/c', 'to' => '/d', 'code' => 301]], $this->stored($this->siteId));
        self::assertSame([['from' => '/c', 'to' => '/d', 'code' => 301]], $this->lastCall('sites.apply')['redirects']);
        $calls = $this->calls('sites.apply');
        $this->post($this->url('/delete'), ['from' => '/a']);
        self::assertStringContainsString('does not exist', (string) $this->flash('error'));
        self::assertSame($calls, $this->calls('sites.apply'));
    }

    public function testInvalidRedirectsStayInThePanel(): void
    {
        $this->login('cust');
        $this->post($this->url(), ['code' => '301', 'from' => '/ok', 'to' => '/fine']);
        $applied = $this->calls('sites.apply');
        $bad = [
            ['code' => '301', 'from' => '', 'to' => '/x'],
            ['code' => '301', 'from' => '/a b', 'to' => '/x'],
            ['code' => '301', 'from' => '/a;b', 'to' => '/x'],
            ['code' => '301', 'from' => '/a/../b', 'to' => '/x'],
            ['code' => '301', 'from' => '/a//b', 'to' => '/x'],
            ['code' => '301', 'from' => "/a\n/b", 'to' => '/x'],
            ['code' => '301', 'from' => '/x', 'to' => ''],
            ['code' => '301', 'from' => '/x', 'to' => 'javascript:alert(1)'],
            ['code' => '301', 'from' => '/x', 'to' => 'ftp://example.com/'],
            ['code' => '301', 'from' => '/x', 'to' => "https://example.com/a\r\nSet-Cookie: x=1"],
            ['code' => '301', 'from' => '/x', 'to' => 'https://example.com/"; return 200 "'],
            ['code' => '301', 'from' => '/x', 'to' => '/x'],
            ['code' => '301', 'from' => '/x', 'to' => 'https://shop.test/x'],
            ['code' => '999', 'from' => '/x', 'to' => '/y'],
            ['code' => '301', 'from' => '/ok', 'to' => '/other'],
        ];
        foreach ($bad as $i => $in) {
            $this->post($this->url(), $in);
            self::assertNotEmpty((array) $this->flash('errors'), 'case ' . $i . ' must be rejected');
        }
        self::assertSame($applied, $this->calls('sites.apply'), 'nothing reached the agent');
        self::assertCount(1, $this->stored($this->siteId));
    }

    public function testTheLimitIsEnforced(): void
    {
        $this->login('cust');
        $many = [];
        for ($i = 0; $i < 100; $i++) {
            $many[] = ['from' => '/p' . $i, 'to' => '/q' . $i, 'code' => 301];
        }
        $this->db->run('UPDATE websites SET redirects = ? WHERE id = ?', [json_encode($many), $this->siteId]);
        $before = $this->calls('sites.apply');
        $this->post($this->url(), ['code' => '301', 'from' => '/one-more', 'to' => '/there']);
        self::assertNotEmpty((array) $this->flash('errors'));
        self::assertSame($before, $this->calls('sites.apply'));
    }

    public function testARefusedChangeLeavesTheSavedRedirects(): void
    {
        $this->login('cust');
        $this->post($this->url(), ['code' => '301', 'from' => '/keep', 'to' => '/kept']);
        $saved = $this->stored($this->siteId);
        $this->agent->fail('sites.apply', 409, 'failed', 'The website configuration was not applied: nginx -t failed.');
        $this->post($this->url(), ['code' => '301', 'from' => '/new', 'to' => '/newer']);
        self::assertStringContainsString('not applied', (string) $this->flash('error'));
        self::assertSame($saved, $this->stored($this->siteId));
        $this->post($this->url('/delete'), ['from' => '/keep']);
        self::assertSame($saved, $this->stored($this->siteId));
    }

    public function testOwnersManageTheirOwnWebsitesOnly(): void
    {
        $this->switchTo('cust');
        self::assertSame(404, $this->request('GET', '/sites/' . $this->otherSite . '/redirects')->status());
        $this->post('/sites/' . $this->otherSite . '/redirects', ['code' => '301', 'from' => '/a', 'to' => '/b']);
        $this->post('/sites/' . $this->otherSite . '/redirects/delete', ['from' => '/a']);
        self::assertSame(0, $this->calls('sites.apply'));
        self::assertSame([], $this->stored($this->otherSite));
    }

    public function testTheSettingsPageNoLongerHasTheTextAreaOrThePhpSections(): void
    {
        $this->login('cust');
        $form = $this->request('GET', '/sites/' . $this->siteId . '/settings')->body();
        self::assertStringNotContainsString('name="redirects"', $form);
        self::assertStringNotContainsString('Additional PHP directives', $form);
        self::assertStringNotContainsString('name="memory_limit_mb"', $form);
        self::assertStringNotContainsString('name="php_version"', $form);
        self::assertStringNotContainsString('name="ini[', $form);
        self::assertStringContainsString('/sites/' . $this->siteId . '/redirects', $form, 'points to the Redirects page');
        self::assertStringContainsString('/sites/' . $this->siteId . '/php', $form, 'points to the PHP settings page');
        // New websites still choose their PHP version when created.
        self::assertStringContainsString('name="php_version"', $this->request('GET', '/websites/new')->body());
    }

    public function testSavingTheSettingsKeepsRedirectsAndPhpSettings(): void
    {
        $this->login('cust');
        $this->post($this->url(), ['code' => '301', 'from' => '/keep', 'to' => '/kept']);
        $this->db->run("UPDATE websites SET php_settings = ? WHERE id = ?", [json_encode(['memory_limit_mb' => 512, 'ini' => ['date.timezone' => 'Asia/Karachi']]), $this->siteId]);
        $this->post('/sites/' . $this->siteId . '/settings', ['aliases' => "www.shop.test"]);
        $spec = $this->lastCall('sites.apply');
        self::assertSame([['from' => '/keep', 'to' => '/kept', 'code' => 301]], $spec['redirects']);
        self::assertSame(512, $spec['php']['memory_limit_mb']);
        self::assertSame(['date.timezone' => 'Asia/Karachi'], $spec['php']['ini']);
        self::assertSame('8.3', $spec['php_version']);
    }

    public function testThePhpSettingsPage(): void
    {
        $this->login('cust');
        $overview = $this->request('GET', '/sites/' . $this->siteId)->body();
        self::assertStringContainsString('href="/sites/' . $this->siteId . '/php"', $overview, 'PHP settings tile');
        self::assertStringNotContainsString('settings#php', $overview);
        $page = $this->request('GET', '/sites/' . $this->siteId . '/php')->body();
        foreach (['PHP version', 'name="php_version"', 'name="memory_limit_mb"', 'name="upload_max_mb"', 'name="ini[date.timezone]"', 'Additional PHP directives'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertStringNotContainsString('name="redirects"', $page);
        self::assertStringNotContainsString('name="aliases"', $page, 'nothing but PHP settings');

        // Saving changes the PHP settings only; aliases and redirects stay.
        $this->db->run("UPDATE websites SET redirects = ? WHERE id = ?", [json_encode([['from' => '/keep', 'to' => '/kept', 'code' => 301]]), $this->siteId]);
        $this->db->run("INSERT INTO domains (server_id, website_id, name, is_primary) VALUES (1, ?, 'www.shop.test', 0)", [$this->siteId]);
        $this->post('/sites/' . $this->siteId . '/php', ['memory_limit_mb' => '384', 'upload_max_mb' => '100', 'ini' => ['date.timezone' => 'Europe/Berlin'], 'domain' => 'evil.test', 'redirects' => '/x /y', 'aliases' => 'hijack.test']);
        self::assertStringContainsString('PHP settings saved', (string) $this->flash('success'));
        $spec = $this->lastCall('sites.apply');
        self::assertSame(384, $spec['php']['memory_limit_mb']);
        self::assertSame(100, $spec['php']['upload_max_mb']);
        self::assertSame(['date.timezone' => 'Europe/Berlin'], $spec['php']['ini']);
        self::assertSame('shop.test', $spec['domain'], 'other fields posted here are ignored');
        self::assertSame(['www.shop.test'], $spec['aliases']);
        self::assertSame([['from' => '/keep', 'to' => '/kept', 'code' => 301]], $spec['redirects']);
    }

    public function testThePhpPageIsForPhpWebsitesOfTheOwnerOnly(): void
    {
        $this->switchTo('cust');
        $r = $this->request('GET', '/sites/' . $this->staticSite . '/php');
        self::assertContains($r->status(), [302, 303]);
        $this->post('/sites/' . $this->staticSite . '/php', ['memory_limit_mb' => '300']);
        self::assertSame(0, $this->calls('sites.apply'));
        self::assertSame(404, $this->request('GET', '/sites/' . $this->otherSite . '/php')->status());
        $this->post('/sites/' . $this->otherSite . '/php', ['memory_limit_mb' => '300']);
        self::assertSame(0, $this->calls('sites.apply'));
        // A customer cannot change the administrator's process limits here.
        $this->post('/sites/' . $this->siteId . '/php', ['memory_limit_mb' => '300', 'max_children' => '150', 'pm' => 'static']);
        $spec = $this->lastCall('sites.apply');
        self::assertSame(300, $spec['php']['memory_limit_mb']);
        self::assertNotSame(150, $spec['php']['max_children']);
        self::assertNotSame('static', $spec['php']['pm']);
    }
}
