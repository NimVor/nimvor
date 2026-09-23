<?php

declare(strict_types=1);

namespace WhaCP\Http;

/**
 * Immutable-ish HTTP request (attributes may be attached by middleware).
 */
final class Request
{
    /** @var array<string, mixed> */
    private array $attributes = [];

    /** @var array<string, mixed>|null */
    private ?array $json = null;

    /**
     * @param array<string, mixed> $query
     * @param array<string, mixed> $body
     * @param array<string, mixed> $server
     * @param array<string, string> $headers lower-case names
     */
    public function __construct(
        private readonly string $method,
        private readonly string $path,
        private readonly array $query = [],
        private readonly array $body = [],
        private readonly array $server = [],
        private readonly array $headers = [],
        private readonly string $rawBody = '',
    ) {
    }

    public static function fromGlobals(): self
    {
        $headers = [];
        foreach ($_SERVER as $key => $value) {
            if (is_string($value) && str_starts_with($key, 'HTTP_')) {
                $headers[strtolower(str_replace('_', '-', substr($key, 5)))] = $value;
            }
        }
        if (isset($_SERVER['CONTENT_TYPE'])) {
            $headers['content-type'] = (string) $_SERVER['CONTENT_TYPE'];
        }
        $uri = (string) ($_SERVER['REQUEST_URI'] ?? '/');
        $path = parse_url($uri, PHP_URL_PATH);
        $raw = (string) file_get_contents('php://input', false, null, 0, 1048576);

        return new self(
            strtoupper((string) ($_SERVER['REQUEST_METHOD'] ?? 'GET')),
            is_string($path) && $path !== '' ? $path : '/',
            $_GET,
            $_POST,
            $_SERVER,
            $headers,
            $raw,
        );
    }

    public function method(): string
    {
        return $this->method;
    }

    public function path(): string
    {
        $p = '/' . trim($this->path, '/');
        return $p;
    }

    public function query(string $key, mixed $default = null): mixed
    {
        return $this->query[$key] ?? $default;
    }

    /** @return array<string, mixed> */
    public function queryAll(): array
    {
        return $this->query;
    }

    /**
     * Input from the form body or, for JSON requests, the decoded JSON body.
     */
    public function input(string $key, mixed $default = null): mixed
    {
        if (array_key_exists($key, $this->body)) {
            return $this->body[$key];
        }
        return $this->json()[$key] ?? $default;
    }

    public function string(string $key, string $default = ''): string
    {
        $v = $this->input($key, $default);
        return is_scalar($v) ? trim((string) $v) : $default;
    }

    /** @return array<string, mixed> */
    public function all(): array
    {
        return array_merge($this->json(), $this->body);
    }

    /** @return array<string, mixed> */
    public function json(): array
    {
        if ($this->json === null) {
            $this->json = [];
            if (str_contains($this->header('content-type'), 'application/json') && $this->rawBody !== '') {
                $decoded = json_decode($this->rawBody, true);
                if (is_array($decoded)) {
                    $this->json = $decoded;
                }
            }
        }
        return $this->json;
    }

    public function header(string $name, string $default = ''): string
    {
        return $this->headers[strtolower($name)] ?? $default;
    }

    public function server(string $key, mixed $default = null): mixed
    {
        return $this->server[$key] ?? $default;
    }

    /**
     * Client IP. X-Forwarded-For is honoured only when the direct peer is a
     * trusted proxy.
     *
     * @param list<string> $trustedProxies
     */
    public function ip(array $trustedProxies = []): string
    {
        $remote = (string) ($this->server['REMOTE_ADDR'] ?? '0.0.0.0');
        if ($trustedProxies !== [] && in_array($remote, $trustedProxies, true)) {
            $forwarded = $this->header('x-forwarded-for');
            if ($forwarded !== '') {
                $parts = array_map('trim', explode(',', $forwarded));
                $candidate = (string) end($parts);
                if (filter_var($candidate, FILTER_VALIDATE_IP)) {
                    return $candidate;
                }
            }
        }
        return $remote;
    }

    public function userAgent(): string
    {
        return mb_substr($this->header('user-agent'), 0, 255);
    }

    public function isSecure(): bool
    {
        $https = $this->server['HTTPS'] ?? '';
        return ($https !== '' && $https !== 'off') || (int) ($this->server['SERVER_PORT'] ?? 0) === 443;
    }

    public function wantsJson(): bool
    {
        return str_contains($this->header('accept'), 'application/json')
            || $this->header('x-requested-with') === 'XMLHttpRequest';
    }

    public function isSafeMethod(): bool
    {
        return in_array($this->method, ['GET', 'HEAD', 'OPTIONS'], true);
    }

    public function withAttribute(string $key, mixed $value): self
    {
        $clone = clone $this;
        $clone->attributes[$key] = $value;
        return $clone;
    }

    public function attribute(string $key, mixed $default = null): mixed
    {
        return $this->attributes[$key] ?? $default;
    }
}
