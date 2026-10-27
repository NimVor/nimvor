<?php

declare(strict_types=1);

namespace NimVor\Support;

/**
 * Minimal synchronous event dispatcher (hooks for services and plugins).
 *
 * Events used by the core: website.created, website.updated,
 * website.deleting (before removal, listeners clean up dependent objects),
 * website.deleted, database.created, backup.completed, deploy.completed.
 */
final class Events
{
    /** @var array<string, list<callable(array<string, mixed>): void>> */
    private array $listeners = [];

    /** @param callable(array<string, mixed>): void $listener */
    public function on(string $event, callable $listener): void
    {
        $this->listeners[$event][] = $listener;
    }

    /** @param array<string, mixed> $payload */
    public function dispatch(string $event, array $payload): void
    {
        foreach ($this->listeners[$event] ?? [] as $l) {
            $l($payload);
        }
    }
}
