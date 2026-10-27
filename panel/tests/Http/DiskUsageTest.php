<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Repositories\UserRepository;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/** Disk usage by folder: a background job per folder, results kept per folder. */
final class DiskUsageTest extends HttpTestCase
{
    private int $site;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $this->seedUser('other', 'customer');
        $this->agent->succeed('sites.create', ['ok' => true]);
        $this->agent->succeed('sites.apply', ['ok' => true]);
        $users = $this->app->container->get(UserRepository::class);
        $this->site = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $users->find($cust));
        $this->agent->succeed('sites.disk_usage', ['job_id' => 'abcdef0123456789', 'status' => 'queued']);
    }

    public function testMeasureAndShowFolders(): void
    {
        $this->login('cust');
        $page = $this->request('GET', '/sites/' . $this->site . '/disk-usage')->body();
        self::assertStringContainsString('Not measured yet', $page);
        $this->post('/sites/' . $this->site . '/disk-usage', ['path' => '']);
        self::assertSame(['domain' => 'shop.test', 'path' => ''], $this->lastCall('sites.disk_usage'));
        self::assertSame('abcdef0123456789', $this->session->pullFlash('job'));
        $this->agent->succeed('jobs.get', ['id' => 'abcdef0123456789', 'status' => 'running']);
        self::assertStringContainsString('Measuring', $this->request('GET', '/sites/' . $this->site . '/disk-usage')->body());
        $this->post('/sites/' . $this->site . '/disk-usage', ['path' => '']);
        self::assertSame(1, $this->calls('sites.disk_usage'), 'one scan per folder at a time');

        $this->agent->succeed('jobs.get', ['id' => 'abcdef0123456789', 'status' => 'succeeded', 'result' => ['path' => '', 'bytes' => 3000, 'entries' => [
            ['name' => 'logs', 'kind' => 'dir', 'bytes' => 1000, 'files' => 3],
            ['name' => 'public_html', 'kind' => 'dir', 'bytes' => 1500, 'files' => 40],
            ['name' => 'mail', 'kind' => 'mail', 'bytes' => 400, 'files' => 9],
            ['name' => '', 'kind' => 'files', 'bytes' => 100, 'files' => 1],
            ['name' => '<script>', 'kind' => 'evil', 'bytes' => -5, 'files' => 0],
        ]]]);
        $page = $this->request('GET', '/sites/' . $this->site . '/disk-usage', [], [], '', ['sort' => 'name'])->body();
        foreach (['Email (mailboxes)', 'Files in this folder', '&lt;script&gt;', 'path=public_html', 'value="1500" max="3000"', 'aria-sort="ascending"'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertLessThan(strpos($page, '>logs<'), strpos($page, '&lt;script&gt;'), 'sorted by name');
        $this->post('/sites/' . $this->site . '/disk-usage', ['path' => 'public_html/wp-content']);
        self::assertSame('public_html/wp-content', $this->lastCall('sites.disk_usage')['path']);
        $n = $this->calls('sites.disk_usage');
        $this->post('/sites/' . $this->site . '/disk-usage', ['path' => '../../etc']);
        self::assertSame($n, $this->calls('sites.disk_usage'), 'no path outside the website');
    }

    public function testOthersCannotMeasure(): void
    {
        $this->login('other');
        self::assertSame(404, $this->request('GET', '/sites/' . $this->site . '/disk-usage')->status());
        $this->post('/sites/' . $this->site . '/disk-usage', ['path' => '']);
        self::assertSame(0, $this->calls('sites.disk_usage'));
    }
}
