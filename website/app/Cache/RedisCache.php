<?php

declare(strict_types=1);

namespace NimVorSite\Cache;

final class RedisCache implements Cache
{
    private \Redis $redis;

    /** @param array{host: string, port: int, password: ?string} $c */
    public function __construct(array $c, private readonly string $prefix)
    {
        $this->redis = new \Redis();
        if (!$this->redis->connect($c['host'], $c['port'], 2.0)) {
            throw new \RuntimeException('Redis is not reachable.');
        }
        if (($c['password'] ?? null) !== null && $c['password'] !== '') {
            $this->redis->auth($c['password']);
        }
    }

    public function get(string $key): mixed
    {
        $v = $this->redis->get($this->prefix . $key);
        return $v === false ? null : json_decode((string) $v, true);
    }

    public function set(string $key, mixed $value, int $ttl = 0): void
    {
        $json = json_encode($value, JSON_THROW_ON_ERROR);
        $ttl > 0 ? $this->redis->setex($this->prefix . $key, $ttl, $json) : $this->redis->set($this->prefix . $key, $json);
    }

    public function delete(string $key): void
    {
        $this->redis->del($this->prefix . $key);
    }

    public function increment(string $key, int $ttl): int
    {
        $n = (int) $this->redis->incr($this->prefix . $key);
        if ($n === 1) {
            $this->redis->expire($this->prefix . $key, $ttl);
        }
        return $n;
    }
}
