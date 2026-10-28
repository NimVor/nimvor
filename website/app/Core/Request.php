<?php

declare(strict_types=1);

namespace NimVorSite\Core;

final class Request
{
    /**
     * @param array<string, mixed> $query
     * @param array<string, mixed> $post
     * @param array<string, string> $cookies
     * @param array<string, string> $server
     */
    public function __construct(
        public readonly string $method,
        public readonly string $path,
        public readonly array $query = [],
        public readonly array $post = [],
        public readonly array $cookies = [],
        public readonly array $server = [],
    ) {
    }

    public static function fromGlobals(): self
    {
        $uri = (string) ($_SERVER['REQUEST_URI'] ?? '/');
        $path = rawurldecode((string) parse_url($uri, PHP_URL_PATH));
        return new self(strtoupper((string) ($_SERVER['REQUEST_METHOD'] ?? 'GET')), $path === '' ? '/' : $path, $_GET, $_POST, $_COOKIE, array_map('strval', array_filter($_SERVER, 'is_scalar')));
    }

    public function input(string $key, string $default = ''): string
    {
        $v = $this->post[$key] ?? $this->query[$key] ?? $default;
        return is_string($v) ? $v : $default;
    }

    public function isPost(): bool
    {
        return $this->method === 'POST';
    }

    /** The client address; X-Forwarded-For only counts from the trusted proxy. */
    public function ip(string $trustedProxy = '127.0.0.1'): string
    {
        $remote = $this->server['REMOTE_ADDR'] ?? '0.0.0.0';
        $fwd = $this->server['HTTP_X_FORWARDED_FOR'] ?? '';
        if ($fwd !== '' && $remote === $trustedProxy) {
            $first = trim(explode(',', $fwd)[0]);
            if (filter_var($first, FILTER_VALIDATE_IP) !== false) {
                return $first;
            }
        }
        return filter_var($remote, FILTER_VALIDATE_IP) !== false ? $remote : '0.0.0.0';
    }

    public function isHttps(string $trustedProxy = '127.0.0.1'): bool
    {
        if (($this->server['HTTPS'] ?? '') !== '' && $this->server['HTTPS'] !== 'off') {
            return true;
        }
        return ($this->server['REMOTE_ADDR'] ?? '') === $trustedProxy && ($this->server['HTTP_X_FORWARDED_PROTO'] ?? '') === 'https';
    }

    public function cookie(string $name): ?string
    {
        $v = $this->cookies[$name] ?? null;
        return is_string($v) ? $v : null;
    }
}
