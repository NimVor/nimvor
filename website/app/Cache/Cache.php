<?php

declare(strict_types=1);

namespace NimVorSite\Cache;

/** Sessions, rate limits and cached pages. Values are JSON-encoded. */
interface Cache
{
    public function get(string $key): mixed;

    public function set(string $key, mixed $value, int $ttl = 0): void;

    public function delete(string $key): void;

    /** Adds 1 and returns the new count; the first increment starts the $ttl window. */
    public function increment(string $key, int $ttl): int;
}
