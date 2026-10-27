<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

final class CacheTest extends HttpTestCase
{
    private int $siteId;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->siteId = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3', 'system_user' => 'shopuser'], (array) $this->app->container->get(UserRepository::class)->find($admin));
        $this->agent->succeed('redis.keys', ['keys' => [], 'cursor' => '0', 'db_size' => 0]);
        $this->agent->succeed('redis.key_edit', ['ok' => true]);
        $this->agent->succeed('redis.key_delete', ['deleted' => 1]);
    }

    public function testSiteBrowserIsConfinedToTheSitePrefix(): void
    {
        $body = $this->request('GET', '/sites/' . $this->siteId . '/redis')->body();
        self::assertStringContainsString('data-prefix="shopuser:"', $body);
        self::assertStringContainsString('data-fixed="1"', $body);

        $api = '/sites/' . $this->siteId . '/redis/api';
        $this->post($api, ['action' => 'keys', 'pattern' => '*', 'prefix' => '', 'db' => '2']);
        $call = $this->lastCall('redis.keys');
        self::assertSame('shopuser:', $call['prefix'], 'the prefix cannot be overridden from the browser');
        self::assertSame(2, $call['db']);

        $this->post($api, ['action' => 'delete', 'keys' => [base64_encode('shopuser:a')]]);
        self::assertSame('shopuser:', $this->lastCall('redis.key_delete')['prefix']);
        $db = $this->app->container->get(Connection::class);
        self::assertSame(1, (int) $db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'redis.key_delete'"));
        self::assertSame(400, $this->post($api, ['action' => 'flush'])->status());
    }

    public function testAdminBrowserAndPermissions(): void
    {
        self::assertStringContainsString('data-fixed="0"', $this->request('GET', '/redis/browser')->body());
        $this->post('/redis/browser/api', ['action' => 'keys', 'prefix' => 'any:', 'pattern' => 'x*']);
        self::assertSame('any:', $this->lastCall('redis.keys')['prefix']);

        $this->seedUser('cust', 'customer');
        $this->login('cust');
        self::assertSame(403, $this->request('GET', '/redis/browser')->status());
        self::assertSame(404, $this->request('GET', '/sites/' . $this->siteId . '/redis')->status(), 'not their website');
    }

    public function testMemcached(): void
    {
        $this->agent->succeed('memcached.status', ['installed' => false, 'config_file' => '/etc/memcached.conf', 'settings' => [], 'active' => false]);
        self::assertStringContainsString('Install Memcached', $this->request('GET', '/memcached')->body());

        $this->agent->succeed('memcached.status', ['installed' => true, 'active' => true, 'config_file' => '/etc/memcached.conf',
            'settings' => ['memory_mb' => 64, 'max_conns' => 1024, 'listen' => ['127.0.0.1'], 'port' => 11211],
            'stats' => ['version' => '1.6.24', 'get_hits' => '90', 'get_misses' => '10', 'limit_maxbytes' => '67108864', 'bytes' => '1024', 'curr_items' => '3', 'uptime' => '100'],
            'runtime' => ['maxconns' => '1024']]);
        $body = $this->request('GET', '/memcached')->body();
        self::assertStringContainsString('90 %', $body);
        self::assertStringContainsString('1.6.24', $body);

        $this->agent->succeed('memcached.configure', ['settings' => []]);
        $this->post('/memcached/configure', ['memory_mb' => '128', 'max_conns' => '2048', 'listen' => '127.0.0.1, ::1', 'confirm' => '1']);
        $call = $this->lastCall('memcached.configure');
        self::assertSame(['127.0.0.1', '::1'], $call['listen']);
        self::assertTrue($call['confirm_edit_package_config']);
        self::assertSame(128, $call['memory_mb']);

        $this->agent->succeed('memcached.flush', ['flushed' => true]);
        $this->post('/memcached/flush', ['confirm' => 'nope']);
        self::assertSame(0, $this->calls('memcached.flush'));
        $this->post('/memcached/flush', ['confirm' => 'flush']);
        self::assertSame(1, $this->calls('memcached.flush'));
    }
}
