<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

final class FilesTest extends HttpTestCase
{
    private int $siteId;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->siteId = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3'], (array) $this->app->container->get(UserRepository::class)->find($admin));
    }

    public function testManagerAndEditorArePages(): void
    {
        $body = $this->request('GET', '/sites/' . $this->siteId . '/files')->body();
        self::assertStringContainsString('id="wcp-fm"', $body);
        self::assertStringContainsString('data-start="public_html"', $body);
        self::assertStringContainsString('filemanager.js', $body);
        self::assertStringNotContainsString('wcp-sidebar', $body, 'full-screen layout without the sidebar');

        $edit = $this->request('GET', '/sites/' . $this->siteId . '/files/edit', query: ['path' => 'public_html/index.php']);
        self::assertSame(200, $edit->status());
        self::assertStringContainsString('data-path="public_html/index.php"', $edit->body());
        self::assertStringContainsString('path=public_html', $edit->body(), 'closing returns to the folder');
        self::assertSame(404, $this->request('GET', '/sites/' . $this->siteId . '/files/edit', query: ['path' => 'a/../../etc/passwd'])->status());
    }

    public function testApiPassesOnlyKnownOperationsAndTracksJobs(): void
    {
        $api = '/sites/' . $this->siteId . '/files/api';
        $this->agent->succeed('files.list', [['name' => 'index.php', 'type' => 'file', 'size' => 3, 'mode' => '0644', 'mtime' => '2026-01-01T00:00:00Z']]);
        $res = $this->post($api, ['op' => 'list', 'path' => 'public_html']);
        self::assertSame(200, $res->status());
        self::assertSame('shop.test', $this->lastCall('files.list')['domain']);

        self::assertSame(400, $this->post($api, ['op' => 'exec', 'path' => 'x'])->status());

        $this->agent->succeed('files.compress', ['job_id' => 'abcdefabcdefabcd']);
        $this->post($api, ['op' => 'compress', 'paths' => ['public_html'], 'dest' => 'site.zip', 'domain' => 'other.test']);
        $call = $this->lastCall('files.compress');
        self::assertSame('shop.test', $call['domain'], 'the domain always comes from the website');
        self::assertSame('site.zip', $call['dest']);
        $db = $this->app->container->get(Connection::class);
        self::assertSame(1, (int) $db->scalar("SELECT COUNT(*) FROM agent_jobs WHERE job_id = 'abcdefabcdefabcd'"));
        self::assertSame(1, (int) $db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'files.compress'"));

        $this->agent->succeed('files.extract', ['job_id' => 'bbbbbbbbbbbbbbbb']);
        $this->post($api, ['op' => 'extract', 'path' => 'site.zip', 'dest' => 'restore', 'overwrite' => '1']);
        self::assertSame('restore', $this->lastCall('files.extract')['dest']);
    }

    public function testTrashOperations(): void
    {
        $api = '/sites/' . $this->siteId . '/files/api';
        self::assertStringContainsString('id="fm-trash"', $this->request('GET', '/sites/' . $this->siteId . '/files')->body());

        $this->agent->succeed('files.delete', ['deleted' => 1, 'trashed' => true]);
        $this->post($api, ['op' => 'delete', 'paths' => ['public_html/a.txt']]);
        self::assertArrayNotHasKey('permanent', $this->lastCall('files.delete'), 'the trash by default');
        $this->post($api, ['op' => 'delete', 'paths' => ['public_html/a.txt'], 'permanent' => true]);
        self::assertTrue($this->lastCall('files.delete')['permanent']);

        $this->agent->succeed('files.trash_list', ['items' => []]);
        self::assertSame(200, $this->post($api, ['op' => 'trash_list'])->status());
        $this->agent->succeed('files.trash_restore', ['restored' => [['id' => '20260901T120000Z-0a0b0c0d', 'path' => 'public_html/a.txt']]]);
        $this->post($api, ['op' => 'trash_restore', 'ids' => ['20260901T120000Z-0a0b0c0d']]);
        self::assertSame(['20260901T120000Z-0a0b0c0d'], $this->lastCall('files.trash_restore')['ids']);
        $this->agent->succeed('files.trash_empty', ['deleted' => 3]);
        $this->post($api, ['op' => 'trash_empty', 'older_than_days' => 5]);
        self::assertArrayNotHasKey('older_than_days', $this->lastCall('files.trash_empty'), 'the page empties everything; age is the scheduler\'s');
        $this->agent->succeed('files.trash_delete', ['deleted' => 1]);
        $this->post($api, ['op' => 'trash_delete', 'ids' => ['20260901T120000Z-0a0b0c0d']]);

        $db = $this->app->container->get(Connection::class);
        foreach (['files.delete' => 2, 'files.trash_restore' => 1, 'files.trash_empty' => 1, 'files.trash_delete' => 1, 'files.trash_list' => 0] as $action => $n) {
            self::assertSame($n, (int) $db->scalar('SELECT COUNT(*) FROM audit_logs WHERE action = ?', [$action]), $action);
        }
        self::assertSame(1, (int) $db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'files.delete' AND details LIKE '%\"permanent\":true%'"));
    }

    public function testOtherCustomersCannotOpenTheManager(): void
    {
        $this->seedUser('cust', 'customer');
        $this->login('cust');
        self::assertSame(404, $this->request('GET', '/sites/' . $this->siteId . '/files')->status());
        self::assertSame(404, $this->post('/sites/' . $this->siteId . '/files/api', ['op' => 'list', 'path' => ''])->status());
    }
}
