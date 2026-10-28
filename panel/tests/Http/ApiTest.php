<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\ApiTokenService;
use NimVor\Services\WebsiteService;
use NimVor\Repositories\UserRepository;
use NimVor\Tests\Support\HttpTestCase;

final class ApiTest extends HttpTestCase
{
    private function api(string $method, string $path, ?string $token, array $json = [], array $headers = []): Response
    {
        $h = ['content-type' => 'application/json'] + $headers;
        if ($token !== null) {
            $h['authorization'] = 'Bearer ' . $token;
        }
        [$p, $q] = array_pad(explode('?', $path, 2), 2, '');
        parse_str($q, $query);
        return $this->app->handle(new Request($method, $p, $query, [], ['REMOTE_ADDR' => '192.0.2.50'], $h, $json === [] ? '' : (string) json_encode($json)));
    }

    /** @return array<string, mixed> */
    private static function json(Response $r): array
    {
        return (array) json_decode($r->body(), true);
    }

    private function token(int $userId, string $ability = 'write'): string
    {
        return $this->app->container->get(ApiTokenService::class)->create($userId, ['name' => 't' . bin2hex(random_bytes(2)), 'ability' => $ability, 'expires_days' => 30]);
    }

    public function testAuthenticationAndAbilities(): void
    {
        $admin = $this->seedUser('admin', 'admin');
        $write = $this->token($admin);
        $read = $this->token($admin, 'read');

        self::assertSame(401, $this->api('GET', '/api/v1/me', null)->status());
        self::assertSame(401, $this->api('GET', '/api/v1/me', 'nimvor_' . str_repeat('0', 40))->status());
        $res = $this->api('GET', '/api/v1/me', $write);
        self::assertSame(200, $res->status());
        self::assertSame('admin', self::json($res)['data']['username']);
        self::assertNull($res->header('Set-Cookie'), 'the API never starts a session');
        self::assertSame(200, $this->api('GET', '/api/v1/openapi.yaml', null)->status());

        // Read-only tokens cannot change anything.
        $res = $this->api('POST', '/api/v1/websites', $read, ['domain' => 'x.test']);
        self::assertSame(403, $res->status());
        self::assertSame('read_only_token', self::json($res)['error']['code']);

        // No CSRF token is needed (and a session cookie would not help).
        $res = $this->api('POST', '/api/v1/websites', $write, ['domain' => 'shop.test', 'kind' => 'static', 'aliases' => ['www.shop.test']]);
        self::assertSame(201, $res->status(), $res->body());
        $site = self::json($res)['data'];
        self::assertSame(['www.shop.test'], $site['aliases']);
        $res = $this->api('POST', '/api/v1/websites', $write, ['domain' => 'not a domain']);
        self::assertSame(422, $res->status());
        self::assertArrayHasKey('domain', self::json($res)['error']['fields']);
        self::assertSame(200, $this->api('GET', '/api/v1/websites/' . $site['id'], $read)->status());
        self::assertSame(404, $this->api('GET', '/api/v1/websites/999', $read)->status());
        self::assertSame(404, $this->api('GET', '/api/v1/me', $write, [], ['x-nimvor-server' => '42'])->status());

        // Expired tokens stop working.
        $this->app->container->get(Connection::class)->run("UPDATE api_tokens SET expires_at = '2000-01-01T00:00:00+00:00'");
        self::assertSame(401, $this->api('GET', '/api/v1/me', $write)->status());
        $log = (string) json_encode($this->app->container->get(Connection::class)->fetchAll('SELECT * FROM audit_logs'));
        self::assertStringNotContainsString($write, $log, 'tokens are never logged');
    }

    public function testScopeAndPermissions(): void
    {
        $admin = $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $viewer = $this->seedUser('view', 'viewer');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $sites->create(['domain' => 'admin-site.test', 'kind' => 'static'], (array) $users->find($admin));
        $mine = $sites->create(['domain' => 'cust-site.test', 'kind' => 'static'], (array) $users->find($cust));

        $ct = $this->token($cust);
        $list = self::json($this->api('GET', '/api/v1/websites', $ct))['data'];
        self::assertSame(['cust-site.test'], array_column($list, 'domain'));
        $adminSite = (int) $this->app->container->get(Connection::class)->scalar("SELECT id FROM websites WHERE domain = 'admin-site.test'");
        self::assertSame(404, $this->api('GET', '/api/v1/websites/' . $adminSite, $ct)->status());
        self::assertSame(404, $this->api('DELETE', '/api/v1/websites/' . $adminSite, $ct)->status());
        self::assertSame(403, $this->api('GET', '/api/v1/server', $ct)->status(), 'customers lack server.view');

        $vt = $this->token($viewer);
        self::assertSame(403, $this->api('POST', '/api/v1/websites', $vt, ['domain' => 'v.test'])->status(), 'viewer lacks websites.manage');

        $this->agent->succeed('sites.delete', ['warnings' => []]);
        $res = $this->api('DELETE', '/api/v1/websites/' . $mine, $ct);
        self::assertSame(200, $res->status(), $res->body());
        self::assertFalse($this->lastCall('sites.delete')['remove_files'], 'files are kept unless asked');
    }

    public function testRateLimit(): void
    {
        $admin = $this->seedUser('admin', 'admin');
        $this->token($admin);
        $svc = $this->app->container->get(ApiTokenService::class);
        $id = (int) $this->app->container->get(Connection::class)->scalar('SELECT id FROM api_tokens');
        $now = 1_800_000_000;
        for ($i = 0; $i < ApiTokenService::RATE_PER_MINUTE; $i++) {
            self::assertTrue($svc->hit($id, $now));
        }
        self::assertFalse($svc->hit($id, $now));
        self::assertTrue($svc->hit($id, $now + 60), 'a new minute starts a new budget');
    }

    public function testTokenManagementOnProfile(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->post('/profile/tokens', ['name' => 'ci', 'ability' => 'read', 'expires_days' => '30']);
        $token = (string) $this->session->pullFlash('api_token');
        self::assertMatchesRegularExpression('/^nimvor_[a-f0-9]{40}$/', $token);
        $db = $this->app->container->get(Connection::class);
        self::assertSame(hash('sha256', $token), $db->scalar('SELECT token_hash FROM api_tokens'));
        self::assertStringNotContainsString($token, (string) json_encode($db->fetchAll('SELECT * FROM api_tokens')));
        $id = (int) $db->scalar('SELECT id FROM api_tokens');
        $this->post('/profile/tokens/' . $id . '/revoke');
        self::assertSame(0, (int) $db->scalar('SELECT COUNT(*) FROM api_tokens'));
    }
}
