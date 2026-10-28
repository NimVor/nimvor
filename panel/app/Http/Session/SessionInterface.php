<?php

declare(strict_types=1);

namespace NimVor\Http\Session;

interface SessionInterface
{
    public function start(): void;

    public function get(string $key, mixed $default = null): mixed;

    public function set(string $key, mixed $value): void;

    public function remove(string $key): void;

    /** Issue a new session ID (after login / privilege change). */
    public function regenerate(): void;

    /** Destroy all session data. */
    public function invalidate(): void;

    /** Store a value for the next request only. */
    public function flash(string $key, mixed $value): void;

    /** Read (and consume) a flashed value. */
    public function pullFlash(string $key, mixed $default = null): mixed;
}
