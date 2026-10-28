<?php

declare(strict_types=1);

namespace NimVorSite\Cache;

/** For a server without Redis or Memcached: one small file per key, private to the site's account. */
final class FileCache implements Cache
{
    public function __construct(private readonly string $dir, private readonly string $prefix)
    {
        if (!is_dir($dir) && !@mkdir($dir, 0700, true) && !is_dir($dir)) {
            throw new \RuntimeException('The cache folder cannot be created.');
        }
    }

    private function file(string $key): string
    {
        return rtrim($this->dir, '/') . '/' . hash('sha256', $this->prefix . $key) . '.json';
    }

    /** @return array{v: mixed, exp: int}|null */
    private function read(string $key): ?array
    {
        $raw = @file_get_contents($this->file($key));
        $d = $raw === false ? null : json_decode($raw, true);
        if (!is_array($d) || !array_key_exists('v', $d)) {
            return null;
        }
        if (($d['exp'] ?? 0) > 0 && $d['exp'] < time()) {
            @unlink($this->file($key));
            return null;
        }
        return ['v' => $d['v'], 'exp' => (int) ($d['exp'] ?? 0)];
    }

    private function write(string $key, mixed $value, int $exp): void
    {
        $tmp = $this->file($key) . '.' . bin2hex(random_bytes(4)) . '.tmp';
        file_put_contents($tmp, json_encode(['v' => $value, 'exp' => $exp], JSON_THROW_ON_ERROR), LOCK_EX);
        chmod($tmp, 0600);
        rename($tmp, $this->file($key));
    }

    public function get(string $key): mixed
    {
        return $this->read($key)['v'] ?? null;
    }

    public function set(string $key, mixed $value, int $ttl = 0): void
    {
        $this->write($key, $value, $ttl > 0 ? time() + $ttl : 0);
    }

    public function delete(string $key): void
    {
        @unlink($this->file($key));
    }

    public function increment(string $key, int $ttl): int
    {
        $lock = fopen($this->file('lock'), 'c');
        flock($lock, LOCK_EX);
        try {
            $cur = $this->read($key);
            $n = (int) ($cur['v'] ?? 0) + 1;
            $this->write($key, $n, $cur !== null && $cur['exp'] > 0 ? $cur['exp'] : time() + $ttl);
            return $n;
        } finally {
            flock($lock, LOCK_UN);
            fclose($lock);
        }
    }
}
