<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Tests\Support\HttpTestCase;

/** Allow and deny lists of addresses, browsers and URLs: per website and server-wide. */
final class AccessListsTest extends HttpTestCase
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
        $list = [
            ['kind' => 'ip', 'action' => 'deny', 'value' => '203.0.113.0/24', 'note' => 'scanner'],
            ['kind' => 'ua', 'action' => 'deny', 'value' => 'BadBot', 'note' => ''],
            ['kind' => 'url', 'action' => 'allow', 'value' => '/api/', 'note' => ''],
        ];
        $this->agent->succeed('protect.status', ['password' => [], 'hotlink' => null, 'wordpress' => [], 'limits' => [], 'access_status' => 404, 'access' => $list]);
        $this->agent->succeed('protect.access', []);
        $this->agent->succeed('protect.access_global_status', ['access_status' => 403, 'access' => [['kind' => 'ua', 'action' => 'deny', 'value' => 'scrapy', 'note' => '']]]);
        $this->agent->succeed('protect.access_global', ['skipped' => []]);
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    private function url(string $suffix = ''): string
    {
        return '/sites/' . $this->siteId . '/access-lists' . $suffix;
    }

    public function testThePageListsTheEntries(): void
    {
        $this->login('admin');
        self::assertStringContainsString('href="' . $this->url() . '"', $this->request('GET', '/sites/' . $this->siteId)->body(), 'the tile links to the page');
        $page = $this->request('GET', $this->url())->body();
        foreach (['203.0.113.0/24', 'scanner', 'BadBot', '/api/', 'Allow', 'Deny', 'value="404" selected', 'name="kind"', 'name="text"', 'access-lists/export', 'always wins over a deny entry'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
    }

    public function testAddingAnEntrySendsTheWholeList(): void
    {
        $this->login('admin');
        $this->post($this->url('/add'), ['kind' => 'ip', 'action' => 'allow', 'value' => '198.51.100.7', 'note' => 'office']);
        $sent = $this->lastCall('protect.access');
        self::assertSame('shop.test', $sent['domain']);
        self::assertSame(404, $sent['status']);
        self::assertCount(4, $sent['rules']);
        self::assertSame(['kind' => 'ip', 'action' => 'allow', 'value' => '198.51.100.7', 'note' => 'office'], $sent['rules'][3]);
        // The same entry again changes its action instead of adding another.
        $this->post($this->url('/add'), ['kind' => 'ua', 'action' => 'allow', 'value' => 'BadBot']);
        $sent = $this->lastCall('protect.access');
        self::assertCount(3, $sent['rules']);
        self::assertSame('allow', $sent['rules'][1]['action']);
        $audit = (string) $this->app->container->get(Connection::class)->scalar("SELECT details FROM audit_logs WHERE action = 'access_list.add' ORDER BY id DESC LIMIT 1");
        self::assertStringContainsString('198.51.100.7', $audit);
    }

    public function testInvalidEntriesNeverReachTheAgent(): void
    {
        $this->login('admin');
        foreach ([
            ['kind' => 'ip', 'action' => 'deny', 'value' => '999.1.1.1'],
            ['kind' => 'ip', 'action' => 'deny', 'value' => '10.0.0.0/33'],
            ['kind' => 'ip', 'action' => 'deny', 'value' => '10.0.0.9-10.0.0.1'],
            ['kind' => 'ip', 'action' => 'deny', 'value' => 'evil; deny'],
            ['kind' => 'ip', 'action' => 'block', 'value' => '1.2.3.4'],
            ['kind' => 'ua', 'action' => 'deny', 'value' => 'x";return 200;#'],
            ['kind' => 'ua', 'action' => 'deny', 'value' => 'a$b'],
            ['kind' => 'ua', 'action' => 'deny', 'value' => ''],
            ['kind' => 'url', 'action' => 'deny', 'value' => 'nosla'],
            ['kind' => 'url', 'action' => 'deny', 'value' => '/a/../b'],
            ['kind' => 'url', 'action' => 'deny', 'value' => '/a b'],
            ['kind' => 'cookie', 'action' => 'deny', 'value' => 'x'],
        ] as $in) {
            $this->post($this->url('/add'), $in);
        }
        $this->post($this->url('/status'), ['status' => '200']);
        self::assertSame(0, $this->calls('protect.access'));
    }

    public function testRemovingAndTheAnswer(): void
    {
        $this->login('admin');
        $this->post($this->url('/remove'), ['kind' => 'ua', 'value' => 'BadBot']);
        self::assertSame(['203.0.113.0/24', '/api/'], array_column($this->lastCall('protect.access')['rules'], 'value'));
        $this->post($this->url('/status'), ['status' => '444']);
        self::assertSame(444, $this->lastCall('protect.access')['status']);
        $before = $this->calls('protect.access');
        $this->post($this->url('/remove'), ['kind' => 'ip', 'value' => '1.1.1.1']);
        self::assertSame($before, $this->calls('protect.access'), 'an entry that does not exist is not sent');
    }

    public function testImportAndExport(): void
    {
        $this->login('admin');
        $text = "# my list\nallow ip 198.51.100.7  # office\ndeny ip 192.0.2.1-192.0.2.9\ndeny ua SemrushBot\ndeny url /xmlrpc.php\ndeny ip 203.0.113.0/24\n";
        $this->post($this->url('/import'), ['text' => $text, 'mode' => 'merge']);
        $sent = $this->lastCall('protect.access');
        self::assertCount(7, $sent['rules'], 'four new entries are added to the three, one already existed');
        self::assertSame('office', $sent['rules'][3]['note']);
        $this->post($this->url('/import'), ['text' => "deny ip 192.0.2.1\n", 'mode' => 'replace']);
        self::assertSame([['kind' => 'ip', 'action' => 'deny', 'value' => '192.0.2.1', 'note' => '']], $this->lastCall('protect.access')['rules']);

        $before = $this->calls('protect.access');
        foreach (["deny cookie x\n", "block ip 1.2.3.4\n", "deny ip nonsense\n", "# nothing\n", ''] as $bad) {
            $this->post($this->url('/import'), ['text' => $bad, 'mode' => 'merge']);
        }
        self::assertSame($before, $this->calls('protect.access'), 'a bad line stops the whole import');

        $res = $this->request('GET', $this->url('/export'));
        self::assertSame(200, $res->status());
        self::assertStringContainsString('attachment', (string) $res->header('Content-Disposition'));
        self::assertStringContainsString("deny ip 203.0.113.0/24  # scanner\n", $res->body());
        self::assertStringContainsString("allow url /api/\n", $res->body());
    }

    public function testTheServerWideListIsForAdministrators(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/security/access-lists')->body();
        foreach (['Website Access Lists', 'scrapy', 'every website', 'name="kind"'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $this->post('/security/access-lists/add', ['kind' => 'ip', 'action' => 'deny', 'value' => '192.0.2.0/24']);
        $sent = $this->lastCall('protect.access_global');
        self::assertCount(2, $sent['rules']);
        self::assertArrayNotHasKey('domain', $sent);
        $audit = (string) $this->app->container->get(Connection::class)->scalar("SELECT details FROM audit_logs WHERE action = 'access_list.add' ORDER BY id DESC LIMIT 1");
        self::assertStringContainsString('server-wide', $audit);

        $this->switchTo('cust');
        $before = $this->calls('protect.access_global');
        self::assertNotSame(200, $this->request('GET', '/security/access-lists')->status());
        $this->post('/security/access-lists/add', ['kind' => 'ip', 'action' => 'deny', 'value' => '192.0.2.0/24']);
        self::assertSame($before, $this->calls('protect.access_global'), 'customers cannot change the server-wide list');
    }

    public function testOwnersManageTheirOwnSiteOnly(): void
    {
        $this->switchTo('cust');
        self::assertSame(200, $this->request('GET', $this->url())->status());
        $this->post($this->url('/add'), ['kind' => 'ip', 'action' => 'deny', 'value' => '192.0.2.1']);
        self::assertSame(1, $this->calls('protect.access'));
        self::assertSame(404, $this->request('GET', '/sites/' . $this->otherSite . '/access-lists')->status());
        $this->post('/sites/' . $this->otherSite . '/access-lists/add', ['kind' => 'ip', 'action' => 'deny', 'value' => '192.0.2.1']);
        $this->post('/sites/' . $this->otherSite . '/access-lists/import', ['text' => 'deny ip 192.0.2.1', 'mode' => 'replace']);
        self::assertSame(1, $this->calls('protect.access'), "another customer's website is never touched");
        self::assertSame(404, $this->request('GET', '/sites/' . $this->otherSite . '/access-lists/export')->status());
    }
}
