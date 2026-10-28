<?php

declare(strict_types=1);

namespace NimVorSite\Cache;

final class CacheFactory
{
    /** @param array<string, mixed> $c the "cache" section of the configuration */
    public static function make(array $c): Cache
    {
        $prefix = (string) $c['prefix'];
        return match ($c['driver']) {
            'redis' => new RedisCache($c['redis'], $prefix),
            'memcached' => new MemcachedCache($c['memcached'], $prefix),
            'file' => new FileCache((string) $c['file_dir'], $prefix),
            default => throw new \InvalidArgumentException('CACHE_DRIVER must be redis, memcached or file.'),
        };
    }
}
