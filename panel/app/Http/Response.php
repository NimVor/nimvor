<?php

declare(strict_types=1);

namespace NimVor\Http;

final class Response
{
    /** @var (\Closure(): void)|null */
    private ?\Closure $stream = null;

    /** @param array<string, string> $headers */
    public function __construct(
        private string $body = '',
        private int $status = 200,
        private array $headers = [],
    ) {
    }

    public static function html(string $body, int $status = 200): self
    {
        return new self($body, $status, ['Content-Type' => 'text/html; charset=utf-8']);
    }

    /** @param array<mixed>|object $data */
    public static function json(array|object $data, int $status = 200): self
    {
        $body = json_encode($data, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_THROW_ON_ERROR);
        return new self($body, $status, ['Content-Type' => 'application/json; charset=utf-8', 'Cache-Control' => 'no-store']);
    }

    /**
     * Streamed response (large downloads); $writer echoes the body.
     *
     * @param \Closure(): void $writer
     * @param array<string, string> $headers
     */
    public static function stream(\Closure $writer, array $headers): self
    {
        $r = new self('', 200, $headers);
        $r->stream = $writer;
        return $r;
    }

    public static function redirect(string $location, int $status = 303): self
    {
        // Only same-origin, path-absolute redirects are allowed.
        if (!str_starts_with($location, '/') || str_starts_with($location, '//') || str_contains($location, '\\')) {
            $location = '/';
        }
        return new self('', $status, ['Location' => $location]);
    }

    /** Redirect to an external https:// URL (e.g. an OAuth consent page). */
    public static function away(string $url): self
    {
        if (!preg_match('#^https://[^/\\\\\s]+/#', $url) || preg_match('/[\r\n]/', $url)) {
            return self::redirect('/');
        }
        return new self('', 303, ['Location' => $url]);
    }

    public function status(): int
    {
        return $this->status;
    }

    public function body(): string
    {
        return $this->body;
    }

    public function header(string $name): ?string
    {
        foreach ($this->headers as $k => $v) {
            if (strcasecmp($k, $name) === 0) {
                return $v;
            }
        }
        return null;
    }

    /** @return array<string, string> */
    public function headers(): array
    {
        return $this->headers;
    }

    public function withHeader(string $name, string $value): self
    {
        $clone = clone $this;
        $clone->headers[$name] = $value;
        return $clone;
    }

    public function send(): void
    {
        if (!headers_sent()) {
            http_response_code($this->status);
            foreach ($this->headers as $name => $value) {
                header($name . ': ' . str_replace(["\r", "\n"], '', $value), true);
            }
        }
        if ($this->stream !== null) {
            while (ob_get_level() > 0) {
                ob_end_flush();
            }
            ($this->stream)();
            return;
        }
        echo $this->body;
    }
}
