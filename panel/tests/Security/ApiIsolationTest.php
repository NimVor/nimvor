<?php

declare(strict_types=1);

namespace NimVor\Tests\Security;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\ApiTokenService;
use NimVor\Tests\Support\HttpTestCase;

/**
 * Release gate for /api/v1: every write route refuses read-only tokens,
 * and every route taking an id refuses another tenant's resource without
 * reaching the agent.
 */
final class ApiIsolationTest extends HttpTestCase
{
    private function api(string $method, string $path, ?string $token, array $json = []): Response
    {
        $h = ['content-type' => 'application/json'];
        if ($token !== null) {
            $h['authorization'] = 'Bearer ' . $token;
        }
        return $this->app->handle(new Request($method, $path, [], [], ['REMOTE_ADDR' => '192.0.2.60'], $h, $json === [] ? '' : (string) json_encode($json)));
    }

    /** @return list<array{0: string, 1: string}> */
    private static function apiRoutes(): array
    {
        $src = (string) file_get_contents(dirname(__DIR__, 2) . '/routes/web.php');
        preg_match_all("/\\\$r->(?:(get|post)\\(|add\\('([A-Z]+)', )'(\\/api\\/v1\\/[^']*)'/", $src, $m, PREG_SET_ORDER);
        return array_map(static fn (array $x): array => [strtoupper($x[1] !== '' ? $x[1] : $x[2]), $x[3]], $m);
    }

    public function testReadOnlyTokensCannotWriteAnywhere(): void
    {
        $admin = $this->seedUser('admin', 'admin');
        $read = $this->app->container->get(ApiTokenService::class)->create($admin, ['name' => 'ro', 'ability' => 'read', 'expires_days' => 1]);
        $writes = 0;
        foreach (self::apiRoutes() as [$method, $path]) {
            if ($method === 'GET') {
                continue;
            }
            $before = count($this->agent->requests);
            $res = $this->api($method, strtr($path, ['{id}' => '1']), $read, ['domain' => 'ro.test', 'name' => 'ro_db', 'kind' => 'static']);
            self::assertSame(403, $res->status(), "{$method} {$path} with a read-only token");
            self::assertCount($before, $this->agent->requests, "{$method} {$path} reached the agent with a read-only token");
            $writes++;
        }
        self::assertGreaterThanOrEqual(8, $writes);
    }

    public function testCustomersCannotReachOtherTenantsResources(): void
    {
        $alice = $this->seedUser('alice', 'customer');
        $bob = $this->seedUser('bob', 'customer');
        $db = $this->app->container->get(Connection::class);
        $now = gmdate('c');
        $db->run("INSERT INTO websites (server_id, owner_id, domain, system_user, root, document_root, kind, created_at, updated_at) VALUES (1, ?, 'victim.test', 'victim', '/home/nimvor/victim.test', '/home/nimvor/victim.test/public_html', 'static', ?, ?)", [$bob, $now, $now]);
        $victim = (int) $db->scalar("SELECT id FROM websites WHERE domain = 'victim.test'");
        $token = $this->app->container->get(ApiTokenService::class)->create($alice, ['name' => 'rw', 'ability' => 'write', 'expires_days' => 1]);
        foreach (self::apiRoutes() as [$method, $path]) {
            if (!str_contains($path, '{id}') || str_starts_with($path, '/api/v1/jobs')) {
                continue;
            }
            $before = count($this->agent->requests);
            $res = $this->api($method, strtr($path, ['{id}' => (string) $victim]), $token, ['confirm' => 'victim.test']);
            self::assertContains($res->status(), [403, 404], "{$method} {$path} on another tenant's website");
            self::assertCount($before, $this->agent->requests, "{$method} {$path} reached the agent for another tenant's website");
            self::assertStringNotContainsString('victim.test', $res->body());
        }
        $list = (array) json_decode($this->api('GET', '/api/v1/websites', $token)->body(), true);
        self::assertSame([], $list['data'] ?? null, 'the list only has own websites');
    }

    public function testRevokedAndMalformedTokens(): void
    {
        $admin = $this->seedUser('admin', 'admin');
        $svc = $this->app->container->get(ApiTokenService::class);
        $t = $svc->create($admin, ['name' => 'x', 'ability' => 'write', 'expires_days' => 1]);
        self::assertSame(200, $this->api('GET', '/api/v1/me', $t)->status());
        $this->app->container->get(Connection::class)->run('DELETE FROM api_tokens');
        self::assertSame(401, $this->api('GET', '/api/v1/me', $t)->status(), 'a deleted token stops working');
        foreach (['', 'Bearer', 'nimvor_', 'nimvor_' . str_repeat('z', 200), "nimvor_\x00abc", substr($t, 0, -1)] as $bad) {
            self::assertSame(401, $this->api('GET', '/api/v1/me', $bad)->status(), 'token ' . json_encode($bad));
        }
    }
}
