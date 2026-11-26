<?php

declare(strict_types=1);

namespace NimVor\Http\Session;

/**
 * In-memory session used by tests and the CLI.
 */
class ArraySession implements SessionInterface
{
    /** @var array<string, mixed> */
    protected array $data = [];

    public int $regenerations = 0;

    /** Whether close() was called (tests check which requests let go of the session). */
    public bool $closed = false;

    public function start(): void
    {
        $this->closed = false;
    }

    public function close(): void
    {
        $this->closed = true;
    }

    public function get(string $key, mixed $default = null): mixed
    {
        return $this->data[$key] ?? $default;
    }

    public function set(string $key, mixed $value): void
    {
        $this->data[$key] = $value;
    }

    public function remove(string $key): void
    {
        unset($this->data[$key]);
    }

    public function regenerate(): void
    {
        $this->regenerations++;
    }

    public function invalidate(): void
    {
        $this->data = [];
        $this->regenerations++;
    }

    public function flash(string $key, mixed $value): void
    {
        $this->data['_flash'][$key] = $value;
    }

    public function pullFlash(string $key, mixed $default = null): mixed
    {
        $value = $this->data['_flash'][$key] ?? $default;
        unset($this->data['_flash'][$key]);
        return $value;
    }
}
