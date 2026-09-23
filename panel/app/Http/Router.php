<?php

declare(strict_types=1);

namespace WhaCP\Http;

use InvalidArgumentException;

/**
 * Small path router with named routes and per-route permissions.
 */
final class Router
{
    /** @var list<Route> */
    private array $routes = [];

    /** @var array<string, Route> */
    private array $named = [];

    /**
     * @param array{0: class-string, 1: string} $handler
     * @param array{name?: string, permission?: string|null, auth?: bool} $options
     */
    public function add(string $method, string $pattern, array $handler, array $options = []): Route
    {
        $route = new Route(
            strtoupper($method),
            $pattern,
            $handler,
            $options['name'] ?? null,
            $options['permission'] ?? null,
            $options['auth'] ?? true,
        );
        $this->routes[] = $route;
        if ($route->name !== null) {
            if (isset($this->named[$route->name])) {
                throw new InvalidArgumentException("Duplicate route name {$route->name}");
            }
            $this->named[$route->name] = $route;
        }
        return $route;
    }

    /** @param array{0: class-string, 1: string} $handler @param array{name?: string, permission?: string|null, auth?: bool} $options */
    public function get(string $pattern, array $handler, array $options = []): Route
    {
        return $this->add('GET', $pattern, $handler, $options);
    }

    /** @param array{0: class-string, 1: string} $handler @param array{name?: string, permission?: string|null, auth?: bool} $options */
    public function post(string $pattern, array $handler, array $options = []): Route
    {
        return $this->add('POST', $pattern, $handler, $options);
    }

    /**
     * @return array{0: Route|null, 1: array<string, string>, 2: bool} route, params, methodMismatch
     */
    public function match(string $method, string $path): array
    {
        $methodMismatch = false;
        foreach ($this->routes as $route) {
            $params = $route->matches($path);
            if ($params === null) {
                continue;
            }
            if ($route->method === $method || ($method === 'HEAD' && $route->method === 'GET')) {
                return [$route, $params, false];
            }
            $methodMismatch = true;
        }
        return [null, [], $methodMismatch];
    }

    /** @param array<string, string|int> $params */
    public function url(string $name, array $params = [], array $query = []): string
    {
        $route = $this->named[$name] ?? throw new InvalidArgumentException("Unknown route {$name}");
        $url = preg_replace_callback('/\{(\w+)\}/', static function (array $m) use ($params, $name): string {
            if (!array_key_exists($m[1], $params)) {
                throw new InvalidArgumentException("Missing parameter {$m[1]} for route {$name}");
            }
            return rawurlencode((string) $params[$m[1]]);
        }, $route->pattern);
        if ($query !== []) {
            $url .= '?' . http_build_query($query);
        }
        return (string) $url;
    }

    /** @return list<Route> */
    public function routes(): array
    {
        return $this->routes;
    }
}
