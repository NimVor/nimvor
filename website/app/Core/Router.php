<?php

declare(strict_types=1);

namespace NimVorSite\Core;

final class Router
{
    /** @var list<array{method: string, regex: string, pattern: string, handler: callable, auth: ?string}> */
    private array $routes = [];

    /** @param callable(Request, array<string, string>): Response $handler */
    public function add(string $method, string $pattern, callable $handler, ?string $auth = null): void
    {
        $regex = '#^' . preg_replace('#\{([a-z_]+)\}#', '(?P<$1>[^/]+)', $pattern) . '$#';
        $this->routes[] = ['method' => $method, 'regex' => $regex, 'pattern' => $pattern, 'handler' => $handler, 'auth' => $auth];
    }

    /**
     * @return array{route: array{method: string, regex: string, pattern: string, handler: callable, auth: ?string}, params: array<string, string>}|'method'|null
     */
    public function match(string $method, string $path): array|string|null
    {
        $pathMatched = false;
        foreach ($this->routes as $r) {
            if (preg_match($r['regex'], $path, $m) !== 1) {
                continue;
            }
            $pathMatched = true;
            if ($r['method'] !== $method && !($method === 'HEAD' && $r['method'] === 'GET')) {
                continue;
            }
            $params = array_filter($m, 'is_string', ARRAY_FILTER_USE_KEY);
            return ['route' => $r, 'params' => array_map('strval', $params)];
        }
        return $pathMatched ? 'method' : null;
    }
}
