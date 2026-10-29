<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Unit;

use NimVorSite\Cache\RedisCache;
use PHPUnit\Framework\TestCase;

/** Needs a running Redis (REDIS_HOST, default 127.0.0.1). */
final class CacheDriversTest extends TestCase
{
    private function redis(): RedisCache
    {
        return new RedisCache(['host' => getenv('REDIS_HOST') ?: '127.0.0.1', 'port' => 6379, 'password' => null], 'unit:' . bin2hex(random_bytes(4)) . ':');
    }

    public function testRedisStoresDeletesAndCountsWithAWindow(): void
    {
        $c = $this->redis();
        $c->set('k', ['a' => [1, 2]], 30);
        self::assertSame(['a' => [1, 2]], $c->get('k'));
        self::assertNull($c->get('missing'));
        self::assertSame(1, $c->increment('n', 30));
        self::assertSame(2, $c->increment('n', 30));
        self::assertSame(2, $c->get('n'));
        $c->delete('k');
        self::assertNull($c->get('k'));
    }

    public function testRedisKeysExpire(): void
    {
        $c = $this->redis();
        $c->set('short', 1, 1);
        $c->increment('window', 1);
        sleep(2);
        self::assertNull($c->get('short'));
        self::assertSame(1, $c->increment('window', 1), 'the window starts again');
    }

    public function testMemcachedDriverIsOnlyTestedWhereTheExtensionExists(): void
    {
        if (!class_exists(\Memcached::class)) {
            self::markTestSkipped('ext-memcached is not installed here, so the Memcached driver is not exercised.');
        }
        try {
            $c = new \NimVorSite\Cache\MemcachedCache(['host' => getenv('MEMCACHED_HOST') ?: '127.0.0.1', 'port' => 11211], 'unit' . bin2hex(random_bytes(4)) . ':');
        } catch (\RuntimeException) {
            self::markTestSkipped('ext-memcached is installed but no Memcached server answers here, so the Memcached driver is not exercised.');
        }
        $c->set('k', ['v' => 1], 30);
        self::assertSame(['v' => 1], $c->get('k'));
        self::assertSame(1, $c->increment('n', 30));
        self::assertSame(2, $c->increment('n', 30));
    }
}
