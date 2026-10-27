<?php

declare(strict_types=1);

namespace WhaCP\Http;

final class Route
{
    private readonly string $regex;

    /**
     * @param array{0: class-string, 1: string} $handler
     */
    public function __construct(
        public readonly string $method,
        public readonly string $pattern,
        public readonly array $handler,
        public readonly ?string $name,
        public readonly ?string $permission,
        public readonly bool $requiresAuth,
        // Only machine endpoints that authenticate each request themselves
        // (signed webhooks) may opt out of CSRF protection.
        public readonly bool $requiresCsrf = true,
    ) {
        // {id} matches one path segment of safe characters.
        $quoted = preg_quote($pattern, '#');
        $this->regex = '#^' . preg_replace('/\\\\\{(\w+)\\\\\}/', '(?P<$1>[A-Za-z0-9_.-]+)', $quoted) . '$#';
    }

    /** @return array<string, string>|null */
    public function matches(string $path): ?array
    {
        if (!preg_match($this->regex, $path, $m)) {
            return null;
        }
        $params = [];
        foreach ($m as $k => $v) {
            if (is_string($k)) {
                $params[$k] = $v;
            }
        }
        return $params;
    }
}
