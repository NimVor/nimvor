<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Repositories\UserRepository;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/** Directory listing and file permission repair of a website. */
final class SiteFilesToolsTest extends HttpTestCase
{
    private int $site;
    private int $cust;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->seedUser('other', 'customer');
        $this->agent->succeed('sites.apply', ['ok' => true]);
        $users = $this->app->container->get(UserRepository::class);
        $this->site = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $users->find($this->cust));
        $this->agent->succeed('sites.permissions', ['checked' => 120, 'issues' => 2, 'fixed' => 0, 'counts' => ['owner' => 1, 'world_write' => 1], 'sample' => [['path' => 'shop.test/public_html/<b>.php', 'problems' => ['owner'], 'mode' => '-rw-r--r--']], 'skipped_count' => 1, 'dry_run' => true]);
    }

    public function testDirectoryListing(): void
    {
        $this->login('cust');
        self::assertStringContainsString('return “403 Forbidden”', $this->request('GET', '/sites/' . $this->site . '/advanced')->body());
        self::assertArrayNotHasKey('autoindex', $this->lastCall('sites.apply'), 'off: not sent (older agents)');
        $this->post('/sites/' . $this->site . '/directory-listing', ['enabled' => '1']);
        self::assertTrue($this->lastCall('sites.apply')['autoindex']);
        self::assertStringContainsString('Directory listing is on', (string) $this->flash('success'));
        self::assertStringContainsString('Turn off', $this->request('GET', '/sites/' . $this->site . '/advanced')->body());
        $this->post('/sites/' . $this->site . '/directory-listing', ['enabled' => '0']);
        self::assertArrayNotHasKey('autoindex', $this->lastCall('sites.apply'));
    }

    public function testFailedApplyRestoresTheSetting(): void
    {
        $this->login('cust');
        $this->agent->fail('sites.apply', 500, 'failed', 'nginx -t failed');
        $this->post('/sites/' . $this->site . '/directory-listing', ['enabled' => '1']);
        self::assertSame(0, (int) $this->app->container->get(\WhaCP\Database\Connection::class)->scalar('SELECT directory_listing FROM websites WHERE id = ?', [$this->site]));
    }

    public function testPermissionsCheckAndRepair(): void
    {
        $this->login('cust');
        $json = json_decode($this->request('GET', '/sites/' . $this->site . '/permissions', [], ['accept' => 'application/json'])->body(), true);
        self::assertSame(2, $json['issues']);
        $call = $this->lastCall('sites.permissions');
        self::assertTrue($call['dry_run']);
        self::assertSame('shop.test', $call['domain']);
        self::assertStringEndsWith('/shop.test/public_html', $call['document_root']);
        $this->agent->succeed('sites.permissions', ['checked' => 120, 'issues' => 2, 'fixed' => 2, 'counts' => [], 'sample' => [], 'skipped_count' => 1, 'dry_run' => false]);
        $this->post('/sites/' . $this->site . '/permissions');
        self::assertFalse($this->lastCall('sites.permissions')['dry_run']);
        self::assertSame('Checked 120 file(s) and folder(s); repaired 2. 1 link(s) or special file(s) were left as they are.', $this->flash('success'));
    }

    public function testOthersCannotUseTheTools(): void
    {
        $this->login('other');
        $calls = $this->calls('sites.apply');
        $this->post('/sites/' . $this->site . '/directory-listing', ['enabled' => '1']);
        self::assertSame(404, $this->request('GET', '/sites/' . $this->site . '/permissions', [], ['accept' => 'application/json'])->status());
        $this->post('/sites/' . $this->site . '/permissions');
        self::assertSame($calls, $this->calls('sites.apply'));
        self::assertSame(0, $this->calls('sites.permissions'));
    }
}
