<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Api\OpenApi;
use NimVor\Http\Router;
use NimVor\Tests\Support\HttpTestCase;

/**
 * The API description is what the website's API reference shows: it must list every
 * endpoint the router serves, with the permission the router enforces, and the
 * committed openapi.json must be the built one.
 */
final class OpenApiTest extends HttpTestCase
{
    private const METHODS = ['get', 'post', 'put', 'patch', 'delete'];

    /** @return array<string, mixed> */
    private static function committed(): array
    {
        return (array) json_decode((string) file_get_contents(dirname(__DIR__, 2) . '/resources/api/openapi.json'), true, 512, JSON_THROW_ON_ERROR);
    }

    public function testCommittedFileIsTheBuiltOne(): void
    {
        self::assertSame(
            OpenApi::encode(OpenApi::build()),
            (string) file_get_contents(dirname(__DIR__, 2) . '/resources/api/openapi.json'),
            'resources/api/openapi.json is stale: run bin/build-openapi and commit it',
        );
    }

    public function testEveryRouteIsDescribedWithItsPermission(): void
    {
        $spec = self::committed();
        $routes = [];
        foreach ($this->app->container->get(Router::class)->routes() as $r) {
            if (str_starts_with($r->pattern, '/api/v1/') && !str_starts_with($r->pattern, '/api/v1/openapi.')) {
                $routes[strtolower($r->method) . ' ' . substr($r->pattern, strlen('/api/v1'))] = $r->permission;
            }
        }
        $described = [];
        foreach ($spec['paths'] as $path => $item) {
            foreach (self::METHODS as $m) {
                if (isset($item[$m])) {
                    $described[$m . ' ' . $path] = $item[$m]['x-permission'] ?? null;
                }
            }
        }
        self::assertSame([], array_values(array_diff(array_keys($routes), array_keys($described))), 'routes missing from the API description');
        self::assertSame([], array_values(array_diff(array_keys($described), array_keys($routes))), 'described operations without a route');
        foreach ($routes as $key => $permission) {
            self::assertSame($permission, $described[$key], 'x-permission of ' . $key . ' differs from the router');
        }
    }

    public function testOperationsAreCompleteAndReferencesResolve(): void
    {
        $spec = self::committed();
        $tags = array_column($spec['tags'], 'name');
        $ids = [];
        foreach ($spec['paths'] as $path => $item) {
            preg_match_all('/\{(\w+)\}/', $path, $m);
            foreach (self::METHODS as $method) {
                if (!isset($item[$method])) {
                    continue;
                }
                $op = $item[$method];
                $where = strtoupper($method) . ' ' . $path;
                foreach (['operationId', 'summary', 'tags', 'responses'] as $k) {
                    self::assertNotEmpty($op[$k] ?? null, $where . ' lacks ' . $k);
                }
                self::assertArrayNotHasKey($op['operationId'], $ids, 'duplicate operationId ' . $op['operationId']);
                $ids[$op['operationId']] = true;
                foreach ($op['tags'] as $t) {
                    self::assertContains($t, $tags, $where . ' uses an undeclared tag');
                }
                $named = [];
                foreach ($op['parameters'] ?? [] as $p) {
                    $p = isset($p['$ref']) ? self::resolve($spec, $p['$ref']) : $p;
                    $named[$p['in'] . ':' . $p['name']] = true;
                }
                foreach ($m[1] as $param) {
                    self::assertArrayHasKey('path:' . $param, $named, $where . ' does not describe {' . $param . '}');
                }
                foreach ($op['responses'] as $status => $res) {
                    if (isset($res['$ref'])) {
                        self::resolve($spec, $res['$ref']);
                        continue;
                    }
                    if (str_starts_with((string) $status, '2')) {
                        self::assertArrayHasKey('example', $res['content']['application/json'] ?? [], $where . ' ' . $status . ' has no example');
                    }
                }
            }
        }
        // Every $ref anywhere points at something that exists.
        array_walk_recursive($spec, static function ($v, $k) use ($spec): void {
            if ($k === '$ref') {
                self::resolve($spec, (string) $v);
            }
        });
    }

    /**
     * @param array<string, mixed> $spec
     * @return array<string, mixed>
     */
    private static function resolve(array $spec, string $ref): array
    {
        self::assertStringStartsWith('#/', $ref);
        $node = $spec;
        foreach (explode('/', substr($ref, 2)) as $part) {
            self::assertArrayHasKey($part, $node, 'unresolved reference ' . $ref);
            $node = $node[$part];
        }
        return (array) $node;
    }

    public function testApiServesTheDescription(): void
    {
        foreach (['/api/v1/openapi.json' => 'application/json', '/api/v1/openapi.yaml' => 'application/yaml'] as $path => $type) {
            $res = $this->request('GET', $path);
            self::assertSame(200, $res->status());
            self::assertStringContainsString($type, (string) $res->header('Content-Type'));
            self::assertSame('3.1.0', json_decode($res->body(), true)['openapi']);
        }
    }
}
