<?php

declare(strict_types=1);

namespace NimVorSite\Core;

final class Response
{
    /** @var list<string> */
    private array $cookies = [];

    /** @param array<string, string> $headers */
    public function __construct(public int $status = 200, public string $body = '', public array $headers = [])
    {
    }

    public static function html(string $body, int $status = 200): self
    {
        return new self($status, $body, ['Content-Type' => 'text/html; charset=UTF-8']);
    }

    public static function text(string $body, string $type = 'text/plain', int $status = 200): self
    {
        return new self($status, $body, ['Content-Type' => $type . '; charset=UTF-8']);
    }

    public static function redirect(string $to, int $status = 303): self
    {
        // Only local paths: no open redirect.
        if (!str_starts_with($to, '/') || str_starts_with($to, '//') || str_contains($to, "\n") || str_contains($to, "\r")) {
            $to = '/';
        }
        return new self($status, '', ['Location' => $to]);
    }

    public function header(string $name, string $value): self
    {
        $this->headers[$name] = $value;
        return $this;
    }

    public function cookie(string $header): self
    {
        $this->cookies[] = $header;
        return $this;
    }

    /** @return list<string> */
    public function cookies(): array
    {
        return $this->cookies;
    }

    public function send(): void
    {
        http_response_code($this->status);
        foreach ($this->headers as $k => $v) {
            header($k . ': ' . $v);
        }
        foreach ($this->cookies as $c) {
            header('Set-Cookie: ' . $c, false);
        }
        echo $this->body;
    }
}
