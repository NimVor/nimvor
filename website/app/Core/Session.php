<?php

declare(strict_types=1);

namespace NimVorSite\Core;

use NimVorSite\Cache\Cache;

/**
 * A server-side session in the cache. Created only when something is stored
 * (a sign-in, a form's CSRF token, a flash message), so reading public pages
 * writes nothing.
 */
final class Session
{
    public const COOKIE = 'nimvor_sid';

    private ?string $id = null;
    /** @var array<string, mixed> */
    private array $data = [];
    private bool $dirty = false;
    private bool $destroyed = false;

    public function __construct(private readonly Cache $cache, private readonly int $ttl)
    {
    }

    public function load(?string $cookie): void
    {
        if ($cookie !== null && preg_match('/^[a-f0-9]{64}$/', $cookie) === 1) {
            $d = $this->cache->get('sess:' . $cookie);
            if (is_array($d)) {
                $this->id = $cookie;
                $this->data = $d;
            }
        }
    }

    public function get(string $key, mixed $default = null): mixed
    {
        return $this->data[$key] ?? $default;
    }

    public function put(string $key, mixed $value): void
    {
        $this->ensure();
        $this->data[$key] = $value;
        $this->dirty = true;
    }

    public function forget(string $key): void
    {
        if (array_key_exists($key, $this->data)) {
            unset($this->data[$key]);
            $this->dirty = true;
        }
    }

    /** Shown once on the next page. */
    public function flash(string $type, string $message): void
    {
        $this->put('flash', ['type' => $type, 'message' => $message]);
    }

    /** @return array{type: string, message: string}|null */
    public function pullFlash(): ?array
    {
        $f = $this->get('flash');
        if (!is_array($f)) {
            return null;
        }
        $this->forget('flash');
        return ['type' => (string) ($f['type'] ?? 'info'), 'message' => (string) ($f['message'] ?? '')];
    }

    public function csrfToken(): string
    {
        $t = $this->get('csrf');
        if (!is_string($t)) {
            $t = bin2hex(random_bytes(32));
            $this->put('csrf', $t);
        }
        return $t;
    }

    public function checkCsrf(string $token): bool
    {
        $t = $this->get('csrf');
        return is_string($t) && $token !== '' && hash_equals($t, $token);
    }

    /** A new id with the same data: after sign-in, so a fixed id is worthless. */
    public function regenerate(): void
    {
        if ($this->id !== null) {
            $this->cache->delete('sess:' . $this->id);
        }
        $this->id = bin2hex(random_bytes(32));
        $this->dirty = true;
    }

    public function destroy(): void
    {
        if ($this->id !== null) {
            $this->cache->delete('sess:' . $this->id);
        }
        $this->id = null;
        $this->data = [];
        $this->dirty = false;
        $this->destroyed = true;
    }

    private function ensure(): void
    {
        $this->id ??= bin2hex(random_bytes(32));
    }

    /** Stores the session and returns the Set-Cookie header, if one is needed. */
    public function commit(bool $secure): ?string
    {
        if ($this->destroyed) {
            return self::COOKIE . '=; Path=/; Max-Age=0; HttpOnly; SameSite=Lax' . ($secure ? '; Secure' : '');
        }
        if ($this->id === null || !$this->dirty) {
            return null;
        }
        $this->cache->set('sess:' . $this->id, $this->data, $this->ttl);
        return self::COOKIE . '=' . $this->id . '; Path=/; Max-Age=' . $this->ttl . '; HttpOnly; SameSite=Lax' . ($secure ? '; Secure' : '');
    }
}
