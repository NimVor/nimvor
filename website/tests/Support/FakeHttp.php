<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Support;

use NimVorSite\Services\Http;

/** Stands in for GitHub: answers by URL prefix, remembers what was asked, can be "down". */
final class FakeHttp implements Http
{
    /** @var array<string, array{int, string}> */
    private array $routes = [];
    /** @var list<array{method: string, url: string, headers: array<string, string>, body: ?string}> */
    public array $calls = [];
    public bool $down = false;

    public function on(string $prefix, int $status, string|array $body): self
    {
        $this->routes[$prefix] = [$status, is_array($body) ? (string) json_encode($body) : $body];
        return $this;
    }

    public function request(string $method, string $url, array $headers = [], ?string $body = null, int $timeout = 10): array
    {
        $this->calls[] = ['method' => $method, 'url' => $url, 'headers' => $headers, 'body' => $body];
        if ($this->down) {
            throw new \RuntimeException('network down');
        }
        $best = null;
        foreach ($this->routes as $prefix => $r) {
            if (str_starts_with($url, $prefix) && ($best === null || strlen($prefix) > strlen($best))) {
                $best = $prefix;
            }
        }
        if ($best === null) {
            return ['status' => 404, 'body' => 'not found'];
        }
        return ['status' => $this->routes[$best][0], 'body' => $this->routes[$best][1]];
    }

    public function asked(string $needle): int
    {
        return count(array_filter($this->calls, static fn (array $c): bool => str_contains($c['url'], $needle)));
    }
}
