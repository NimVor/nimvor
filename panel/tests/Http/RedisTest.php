<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

final class RedisTest extends HttpTestCase
{
    public function testRedisPage(): void
    {
        $admin = $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->agent->succeed('redis.info', ['info' => ['server' => ['redis_version' => '7.0.15'], 'keyspace' => ['db0' => 'keys=3']], 'maxmemory' => '0', 'maxmemory_policy' => 'noeviction', 'acl_users' => ['default', 'shopuser']]);
        foreach (['redis.configure', 'redis.password', 'redis.flush', 'redis.acl_set', 'redis.acl_del'] as $a) {
            $this->agent->succeed($a, ['ok' => true]);
        }
        $page = $this->request('GET', '/redis')->body();
        self::assertStringContainsString('7.0.15', $page);
        self::assertStringContainsString('shopuser', $page);

        $this->post('/redis/configure', ['maxmemory_mb' => '256', 'policy' => 'allkeys-lru']);
        self::assertSame(['maxmemory_mb' => 256, 'policy' => 'allkeys-lru'], $this->lastCall('redis.configure'));
        $this->post('/redis/configure', ['maxmemory_mb' => '256', 'policy' => 'bogus']);
        self::assertArrayHasKey('policy', (array) $this->flash('errors'));

        $this->post('/redis/password');
        $pw = $this->lastCall('redis.password')['password'];
        self::assertGreaterThanOrEqual(32, strlen($pw));
        self::assertSame($pw, $this->flash('secret')['password']);
        $db = $this->app->container->get(Connection::class);
        self::assertStringNotContainsString($pw, (string) json_encode($db->fetchAll('SELECT * FROM audit_logs')));
        self::assertStringNotContainsString($pw, (string) json_encode($db->fetchAll('SELECT * FROM settings')));

        $flushes = $this->calls('redis.flush');
        $this->post('/redis/flush', ['db' => '0', 'confirm' => 'yes']);
        self::assertSame($flushes, $this->calls('redis.flush'));
        $this->post('/redis/flush', ['db' => '0', 'confirm' => 'db0']);
        self::assertSame($flushes + 1, $this->calls('redis.flush'));

        $this->agent->succeed('sites.apply', ['changed' => []]);
        $siteId = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $this->app->container->get(UserRepository::class)->find($admin));
        $this->post('/redis/users', ['website_id' => (string) $siteId]);
        $call = $this->lastCall('redis.acl_set');
        self::assertSame($call['user'] . ':', $call['prefix']);

        $dels = $this->calls('redis.acl_del');
        $this->post('/redis/users/delete', ['user' => 'default']);
        self::assertSame($dels, $this->calls('redis.acl_del'));
    }

    public function testCustomersCannotManageRedis(): void
    {
        $this->seedUser('cust', 'customer');
        $this->login('cust');
        self::assertSame(403, $this->request('GET', '/redis')->status());
    }
}
