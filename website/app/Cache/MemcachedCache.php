<?php

declare(strict_types=1);

namespace NimVorSite\Cache;

final class MemcachedCache implements Cache
{
    private \Memcached $mc;

    /** @param array{host: string, port: int} $c */
    public function __construct(array $c, private readonly string $prefix)
    {
        $this->mc = new \Memcached();
        $this->mc->addServer($c['host'], $c['port']);
        if ($this->mc->getVersion() === false || in_array(false, $this->mc->getVersion(), true)) {
            throw new \RuntimeException('Memcached is not reachable.');
        }
    }

    public function get(string $key): mixed
    {
        $v = $this->mc->get($this->prefix . $key);
        return $this->mc->getResultCode() === \Memcached::RES_SUCCESS ? json_decode((string) $v, true) : null;
    }

    public function set(string $key, mixed $value, int $ttl = 0): void
    {
        $this->mc->set($this->prefix . $key, json_encode($value, JSON_THROW_ON_ERROR), $ttl);
    }

    public function delete(string $key): void
    {
        $this->mc->delete($this->prefix . $key);
    }

    public function increment(string $key, int $ttl): int
    {
        $k = $this->prefix . $key;
        if ($this->mc->add($k, 1, $ttl)) {
            return 1;
        }
        $n = $this->mc->increment($k);
        return $n === false ? 1 : (int) $n;
    }
}
