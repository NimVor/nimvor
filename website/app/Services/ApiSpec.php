<?php

declare(strict_types=1);

namespace NimVorSite\Services;

use NimVorSite\Repositories\State;

/**
 * The panel's API description (OpenAPI), read from the repository on GitHub and kept in
 * the database, so the API reference always matches the released code and needs no
 * deployment when the API grows.
 */
final class ApiSpec
{
    public const STATE_KEY = 'api:spec';
    public const PATH = 'panel/resources/api/openapi.json';
    /** A refresh is due after this many seconds. */
    public const MAX_AGE = 6 * 3600;

    /** @var array<string, mixed>|null */
    private ?array $memo = null;

    public function __construct(private readonly State $state, private readonly GitHub $github)
    {
    }

    public function due(): bool
    {
        $age = $this->state->age(self::STATE_KEY);
        return $age === null || $age > self::MAX_AGE;
    }

    public function has(): bool
    {
        return $this->state->get(self::STATE_KEY) !== null;
    }

    /** @throws GitHubException when GitHub cannot be read or the file is not an API description; the stored copy stays */
    public function sync(): void
    {
        $raw = $this->github->raw(self::PATH);
        $spec = json_decode($raw, true);
        if (!is_array($spec) || !isset($spec['openapi'], $spec['paths']) || !is_array($spec['paths'])) {
            throw new GitHubException(self::PATH . ' is not an OpenAPI description.');
        }
        $this->state->set(self::STATE_KEY, $raw);
        $this->memo = $spec;
    }

    /** @return array<string, mixed>|null */
    public function spec(): ?array
    {
        if ($this->memo === null) {
            $raw = $this->state->get(self::STATE_KEY);
            $decoded = $raw === null ? null : json_decode($raw, true);
            $this->memo = is_array($decoded) ? $decoded : null;
        }
        return $this->memo;
    }

    /**
     * The operations grouped by tag, in the order the tags are declared.
     *
     * @return list<array{name: string, slug: string, description: string, operations: list<array<string, mixed>>}>
     */
    public function tags(): array
    {
        $spec = $this->spec();
        if ($spec === null) {
            return [];
        }
        $by = [];
        foreach ((array) ($spec['tags'] ?? []) as $t) {
            $by[(string) $t['name']] = ['name' => (string) $t['name'], 'slug' => self::slug((string) $t['name']), 'description' => (string) ($t['description'] ?? ''), 'operations' => []];
        }
        foreach ((array) $spec['paths'] as $path => $item) {
            foreach (['get', 'post', 'put', 'patch', 'delete'] as $method) {
                if (!isset($item[$method]) || !is_array($item[$method])) {
                    continue;
                }
                $op = $item[$method] + ['method' => strtoupper($method), 'path' => (string) $path];
                $tag = (string) (($op['tags'][0] ?? '') ?: 'Other');
                $by[$tag] ??= ['name' => $tag, 'slug' => self::slug($tag), 'description' => '', 'operations' => []];
                $by[$tag]['operations'][] = $op;
            }
        }
        return array_values(array_filter($by, static fn (array $t): bool => $t['operations'] !== []));
    }

    public static function slug(string $name): string
    {
        return trim((string) preg_replace('/[^a-z0-9]+/', '-', strtolower($name)), '-');
    }
}
