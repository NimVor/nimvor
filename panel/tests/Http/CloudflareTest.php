<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Services\CloudflareService;
use NimVor\Support\UserFacingException;
use NimVor\Tests\Support\HttpTestCase;

/** Cloudflare integration: token store + zone/DNS management via a fake API. */
final class CloudflareTest extends HttpTestCase
{
    /** @var list<array{method: string, url: string, body: array<string, mixed>|null}> */
    private array $calls = [];

    private function service(): CloudflareService
    {
        $cf = $this->app->container->get(CloudflareService::class);
        $this->calls = [];
        $cf->setTransport(function (string $method, string $url, ?array $body, string $token): array {
            $this->calls[] = ['method' => $method, 'url' => $url, 'body' => $body];
            $ok = static fn (mixed $result): array => ['status' => 200, 'body' => ['success' => true, 'result' => $result]];
            return match (true) {
                str_contains($url, '/user/tokens/verify') => $ok(['status' => 'active']),
                str_ends_with($url, '/zones?per_page=50&status=active,pending') => $ok([
                    ['id' => str_repeat('a', 32), 'name' => 'shop.test', 'status' => 'active', 'name_servers' => ['ns1.cloudflare.com']],
                ]),
                preg_match('#/zones/[a-f0-9]{32}/settings/ssl$#', $url) === 1 => $ok(['value' => 'full']),
                preg_match('#/zones/[a-f0-9]{32}/settings/always_use_https$#', $url) === 1 => $ok(['value' => 'on']),
                preg_match('#/zones/[a-f0-9]{32}/dns_records#', $url) === 1 => $ok([
                    ['id' => str_repeat('b', 32), 'type' => 'A', 'name' => 'shop.test', 'content' => '203.0.113.5', 'proxied' => false, 'proxiable' => true],
                ]),
                preg_match('#/zones/[a-f0-9]{32}/purge_cache$#', $url) === 1 => $ok(['id' => str_repeat('a', 32)]),
                preg_match('#/zones/[a-f0-9]{32}$#', $url) === 1 => $ok(['id' => str_repeat('a', 32), 'name' => 'shop.test', 'status' => 'active', 'name_servers' => []]),
                default => $ok([]),
            };
        });
        return $cf;
    }

    public function testConnectVerifiesStoresAndManages(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $cf = $this->service();

        $cf->connect('cf-token-abcdef_ABCDEF12345', 1);
        self::assertTrue($cf->connected());
        self::assertStringContainsString('/user/tokens/verify', $this->calls[0]['url']);
        // The token is stored encrypted, never in clear.
        $stored = (string) $this->app->container->get(\NimVor\Database\Connection::class)->scalar('SELECT token_enc FROM cloudflare_accounts');
        self::assertStringNotContainsString('cf-token-abcdef_ABCDEF12345', $stored);

        $zones = $cf->zones();
        self::assertSame('shop.test', $zones[0]['name']);

        $zid = str_repeat('a', 32);
        $zone = $cf->zone($zid);
        self::assertSame('full', $zone['ssl']);
        self::assertTrue($zone['always_https']);
        self::assertSame('A', $zone['records'][0]['type']);

        $cf->setProxied($zid, str_repeat('b', 32), true, 1);
        $last = end($this->calls);
        self::assertSame('PATCH', $last['method']);
        self::assertTrue($last['body']['proxied']);

        $cf->setSslMode($zid, 'strict', 1);
        self::assertSame('strict', end($this->calls)['body']['value']);

        $cf->purge($zid, 1);
        self::assertStringContainsString('/purge_cache', end($this->calls)['url']);

        // Identifiers are validated before any request.
        $before = count($this->calls);
        try {
            $cf->zone('not-a-real-id');
            self::fail('expected a validation error');
        } catch (UserFacingException) {
        }
        self::assertCount($before, $this->calls, 'a bad id makes no API call');
    }

    public function testApiErrorSurfacesMessage(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        $cf = $this->app->container->get(CloudflareService::class);
        $cf->setTransport(static fn (string $m, string $u, ?array $b, string $t): array => ['status' => 403, 'body' => ['success' => false, 'errors' => [['message' => 'Invalid API Token']]]]);
        $this->expectException(UserFacingException::class);
        $this->expectExceptionMessage('Invalid API Token');
        $cf->connect('cf-token-abcdefo1234567890', 1);
    }

    public function testPageAndRoutesAreAdminOnly(): void
    {
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');

        $this->login('admin');
        self::assertStringContainsString('Connect Cloudflare', $this->request('GET', '/cloudflare')->body());

        $this->login('cust');
        self::assertSame(403, $this->request('GET', '/cloudflare')->status());
        self::assertSame(403, $this->post('/cloudflare/connect', ['token' => 'x'])->status());
    }
}
