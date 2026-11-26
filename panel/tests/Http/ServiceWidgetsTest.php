<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Repositories\UserRepository;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** Databases, Redis & Memcached, and Nginx & PHP-FPM dashboard widgets. */
final class ServiceWidgetsTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
    }

    /** @return array<string, mixed> */

    /** The hover card of a Software tile (/dashboard/popover/KEY): its HTML. */
    private function tile(string $key): string
    {
        $res = $this->request('GET', '/dashboard/popover/' . $key, [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        self::assertSame(200, $res->status(), $res->body());
        return (string) ((array) json_decode($res->body(), true))['html'];
    }

    private function widget(string $id): array
    {
        $res = $this->request('GET', '/dashboard/widgets/' . $id, [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        self::assertSame(200, $res->status(), $res->body());
        return (array) json_decode($res->body(), true);
    }

    public function testMariaDbRealNumbersAndMissingOnesAreNA(): void
    {
        $this->agent->succeed('db.server_status', [
            'running' => true, 'unit' => 'mariadb', 'databases' => 4, 'bytes' => 52428800,
            'variables' => ['version' => '10.11.6-MariaDB', 'max_connections' => '100'],
            'status' => ['Uptime' => '90000', 'Threads_connected' => '85', 'Threads_running' => '3', 'Slow_queries' => '12',
                'Innodb_buffer_pool_read_requests' => '1000', 'Innodb_buffer_pool_reads' => '20', 'Created_tmp_tables' => '200', 'Created_tmp_disk_tables' => '50'],
        ]);
        $this->agent->succeed('pg.status', ['installed' => false]);
        $this->login('admin');
        $w = $this->widget('database_overview');
        self::assertSame('ok', $w['state']);
        foreach (['10.11.6-MariaDB', '1 d', '85 / 100', '(3 running)', '98.0%', '25.0%', '50 MB'] as $s) {
            self::assertStringContainsString($s, $w['html']);
        }
        self::assertStringContainsString('text-warning', $w['html'], 'connections are at 85% of the limit');
        self::assertStringNotContainsString('PostgreSQL <span', $w['html'], 'no PostgreSQL section when it is not installed');
    }

    public function testADownDatabaseIsSaidNotZeroed(): void
    {
        $this->agent->succeed('db.server_status', ['running' => false, 'unit' => 'mariadb', 'error' => 'connection refused']);
        $this->agent->succeed('pg.status', ['installed' => false]);
        $this->login('admin');
        $html = $this->widget('database_overview')['html'];
        self::assertStringContainsString('not answering', $html);
        self::assertStringContainsString('connection refused', $html);
        self::assertStringNotContainsString('Slow queries', $html);
    }

    public function testPostgresIsReadFromItsMonitor(): void
    {
        $this->agent->succeed('pg.status', ['installed' => true, 'running' => true, 'version' => '16.2']);
        $this->agent->succeed('pg.monitor', ['uptime_seconds' => 7200, 'max_connections' => 50, 'connections' => ['total' => 12], 'lock_waits' => 1, 'long_running' => [], 'databases' => [['name' => 'a', 'size_bytes' => 1, 'connections' => 1, 'cache_hit_percent' => 99.5], ['name' => 'b', 'size_bytes' => 1, 'connections' => 1, 'cache_hit_percent' => 98.5]]]);
        $this->login('admin');
        $html = $this->widget('database_overview')['html'];
        foreach (['PostgreSQL', '16.2', '2 h', '12 / 50', '99.0%'] as $s) {
            self::assertStringContainsString($s, $html);
        }
    }

    public function testNoAnswerFromAnyDatabaseIsUnavailable(): void
    {
        $this->login('admin');
        self::assertSame('unavailable', $this->widget('database_overview')['state']);
    }

    public function testCaches(): void
    {
        $this->agent->succeed('redis.info', ['info' => ['server' => ['redis_version' => '7.2.4'], 'memory' => ['used_memory' => 10485760], 'clients' => ['connected_clients' => 3],
            'stats' => ['keyspace_hits' => 90, 'keyspace_misses' => 10, 'evicted_keys' => 5], 'keyspace' => ['db0' => 'keys=42,expires=1,avg_ttl=0']], 'maxmemory' => 104857600]);
        $this->agent->succeed('memcached.status', ['installed' => true, 'active' => true, 'stats' => ['bytes' => 1048576, 'limit_maxbytes' => 67108864, 'get_hits' => 1, 'get_misses' => 3, 'curr_connections' => 2, 'evictions' => 0, 'curr_items' => 9]]);
        $this->login('admin');
        $html = $this->tile('redis');
        foreach (['7.2.4', '10 MB / 100 MB', '90.0%', '>42<'] as $s) {
            self::assertStringContainsString($s, $html);
        }
        self::assertStringNotContainsString('Memcached', $html, 'the Redis card shows Redis only');
        $mc = $this->tile('memcached');
        self::assertStringContainsString('25.0%', $mc);
        self::assertStringNotContainsString('7.2.4', $mc);
    }

    public function testMemcachedThatDoesNotAnswerHasNoNumbers(): void
    {
        $this->agent->succeed('memcached.status', ['installed' => true, 'active' => false, 'error' => 'not answering']);
        $this->login('admin');
        $html = $this->tile('memcached');
        self::assertStringContainsString('not running', $html);
        self::assertStringNotContainsString('Hit ratio', $html);
    }

    public function testWebStackShowsNginxAndPhpPoolsFromLogs(): void
    {
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $sites->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3'], (array) $users->find(1));
        $this->agent->succeed('nginx.status', ['installed' => true, 'running' => true, 'config_ok' => false, 'config_error' => 'unexpected "}"']);
        $this->agent->succeed('php.fpm_health', ['versions' => [['version' => '8.3', 'installed' => true, 'active' => true, 'log_read' => true, 'max_children_reached' => 4, 'busy_warnings' => 1, 'child_crashes' => 0, 'last_max_children_pool' => 'shop']]]);
        $this->login('admin');
        $w = ['html' => $this->tile('web')];
        self::assertStringContainsString('The configuration test fails', $w['html']);
        self::assertStringContainsString('8.3', $w['html']);
        self::assertStringContainsString('title="Last: shop"', $w['html']);
        self::assertSame(['8.3'], $this->lastCall('php.fpm_health')['versions']);
    }

    public function testAPoolWhoseLogCannotBeReadShowsNAandNotZero(): void
    {
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $sites->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3'], (array) $users->find(1));
        $this->agent->succeed('nginx.status', ['installed' => true, 'running' => true, 'config_ok' => true]);
        $this->agent->succeed('php.fpm_health', ['versions' => [['version' => '8.3', 'installed' => true, 'active' => true, 'log_read' => false]]]);
        $this->login('admin');
        $html = $this->tile('web');
        self::assertSame(3, preg_match_all('#<td class="text-end[^"]*"[^>]*>N/A</td>#', $html), 'workers exhausted, busy and crashes are unmeasured');
    }
}
