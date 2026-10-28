<?php

declare(strict_types=1);

namespace NimVorSite\Services;

use NimVorSite\Cache\Cache;

final class RateLimiter
{
    public function __construct(private readonly Cache $cache)
    {
    }

    /** Counts one event and tells whether the $limit within $window seconds is now exceeded. */
    public function hit(string $key, int $limit, int $window): bool
    {
        return $this->cache->increment('rl:' . $key, $window) > $limit;
    }

    /** Whether $limit events were already counted (without counting another). */
    public function blocked(string $key, int $limit): bool
    {
        return (int) ($this->cache->get('rl:' . $key) ?? 0) >= $limit;
    }

    public function clear(string $key): void
    {
        $this->cache->delete('rl:' . $key);
    }
}
