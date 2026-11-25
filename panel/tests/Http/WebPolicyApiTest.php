<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\ApiTokenService;
use NimVor\Tests\Support\HttpTestCase;

/** REST API v1: a website's security headers and cache rules. */
final class WebPolicyApiTest extends HttpTestCase
{
    private Connection $db;
    private int $res;
    private int $plain;
    private int $secure;
    private int $php;
    private int $other;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->res = $this->seedUser('res', 'reseller');
        $other = $this->seedUser('other', 'reseller');
        $this->db = $this->app->container->get(Connection::class);
        foreach ([['shop.test', $this->res, 0, 'static'], ['secure.test', $this->res, 1, 'static'], ['app.test', $this->res, 0, 'php'], ['blog.test', $other, 0, 'static']] as [$d, $o, $tls, $kind]) {
            $this->db->run(
                "INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, status, https, force_https, ssl_mode, cert_file, key_file, created_at, updated_at)
                 VALUES (1, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?, 'x', 'x')",
                [$d, $kind, '/home/u/' . $d, '/home/u/' . $d . '/public_html', 'u' . $o, $o, $tls, $tls, $tls ? 'letsencrypt' : 'none', $tls ? '/etc/letsencrypt/live/' . $d . '/fullchain.pem' : '', $tls ? '/etc/letsencrypt/live/' . $d . '/privkey.pem' : '']
            );
        }
        $id = fn (string $d): int => (int) $this->db->scalar('SELECT id FROM websites WHERE domain = ?', [$d]);
        [$this->plain, $this->secure, $this->php, $this->other] = [$id('shop.test'), $id('secure.test'), $id('app.test'), $id('blog.test')];
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->agent->succeed('sites.page_cache_purge', ['purged' => true]);
    }

    /** @param array<string, mixed> $json */
    private function api(string $method, string $path, ?string $token, array $json = []): Response
    {
        $h = ['content-type' => 'application/json'] + ($token === null ? [] : ['authorization' => 'Bearer ' . $token]);
        return $this->app->handle(new Request($method, $path, [], [], ['REMOTE_ADDR' => '192.0.2.80'], $h, $json === [] ? '' : (string) json_encode($json)));
    }

    /** @return array<string, mixed> */
    private static function json(Response $r): array
    {
        return (array) json_decode($r->body(), true);
    }

    private function token(int $userId, string $ability = 'write'): string
    {
        return $this->app->container->get(ApiTokenService::class)->create($userId, ['name' => 't' . bin2hex(random_bytes(2)), 'ability' => $ability, 'all_scopes' => '1', 'expires_days' => 30]);
    }

    /** @return array<string, mixed> what the agent was last asked to apply */
    private function spec(): array
    {
        return $this->lastCall('sites.apply');
    }

    public function testReadShowsTheRecommendedDefaultsAndTheChoices(): void
    {
        $t = $this->token($this->res);
        $res = $this->api('GET', '/api/v1/websites/' . $this->plain . '/web-policy', $t);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame(['sameorigin', 'strict-origin-when-cross-origin', 'restrictive', false, false, false], [$d['headers']['clickjacking'], $d['headers']['referrer'], $d['headers']['permissions'], $d['headers_custom'], $d['cache_custom'], $d['hsts_available']]);
        self::assertContains('deny', $d['options']['clickjacking']);
        self::assertContains('no-store', $d['options']['dynamic_mode']);
        self::assertTrue($this->api('GET', '/api/v1/websites/' . $this->secure . '/web-policy', $t)->status() === 200);
        self::assertTrue(self::json($this->api('GET', '/api/v1/websites/' . $this->secure . '/web-policy', $t))['data']['hsts_available']);
    }

    public function testHeadersChangeOnlyWhatIsSentAndAreApplied(): void
    {
        $t = $this->token($this->res);
        $base = '/api/v1/websites/' . $this->plain . '/web-policy/headers';
        $res = $this->api('PATCH', $base, $t, ['clickjacking' => 'allow', 'frame_ancestors' => ['https://partner.example', 'https://*.example.org'], 'csp' => "default-src 'self'; img-src 'self' https:", 'csp_report_only' => true]);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame(['allow', ['https://partner.example', 'https://*.example.org'], true, true], [$d['headers']['clickjacking'], $d['headers']['frame_ancestors'], $d['headers']['csp_report_only'], $d['headers_custom']]);
        self::assertSame('strict-origin-when-cross-origin', $d['headers']['referrer'], 'what was not sent stays');
        self::assertSame(['allow', ['https://partner.example', 'https://*.example.org']], [$this->spec()['headers']['clickjacking'] ?? null, $this->spec()['headers']['frame_ancestors'] ?? null]);

        // A later change keeps the earlier ones.
        $d = self::json($this->api('PATCH', $base, $t, ['referrer' => 'no-referrer', 'permissions' => 'custom', 'permissions_custom' => 'camera=(), geolocation=(self)']))['data'];
        self::assertSame(['allow', 'no-referrer', 'custom'], [$d['headers']['clickjacking'], $d['headers']['referrer'], $d['headers']['permissions']]);
        self::assertStringContainsString("default-src 'self'", $d['headers']['csp']);
    }

    public function testHstsNeedsWorkingHttpsAndPreloadItsRules(): void
    {
        $t = $this->token($this->res);
        $res = $this->api('PATCH', '/api/v1/websites/' . $this->plain . '/web-policy/headers', $t, ['hsts' => ['enabled' => true]]);
        self::assertSame(422, $res->status());
        self::assertArrayHasKey('hsts_enabled', self::json($res)['error']['fields']);

        $base = '/api/v1/websites/' . $this->secure . '/web-policy/headers';
        $res = $this->api('PATCH', $base, $t, ['hsts' => ['enabled' => true, 'max_age' => 31536000, 'include_subdomains' => true, 'preload' => true]]);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame([true, 31536000, true, true], array_values(self::json($res)['data']['headers']['hsts']));
        self::assertSame(422, $this->api('PATCH', $base, $t, ['hsts' => ['max_age' => 300]])->status(), 'preload needs a year');
        self::assertSame(422, $this->api('PATCH', $base, $t, ['hsts' => ['max_age' => 5]])->status());
    }

    public function testHeaderInputIsValidatedAndNothingIsStored(): void
    {
        $t = $this->token($this->res);
        $base = '/api/v1/websites/' . $this->plain . '/web-policy/headers';
        foreach ([
            ['clickjacking' => 'sometimes'], ['clickjacking' => 'allow'], ['clickjacking' => 'allow', 'frame_ancestors' => ['http://insecure.example']],
            ['csp' => "default-src 'self'; frame-ancestors 'none'"], ['csp' => 'default-src "x"'], ['referrer' => 'whatever'],
            ['permissions' => 'custom', 'permissions_custom' => 'not a policy'], ['clickjacking' => 5], ['csp_report_only' => 'yes'],
            ['frame_ancestors' => 'https://a.example'], ['colour' => 'red'], ['hsts' => 'on'], ['hsts' => ['bogus' => 1]],
        ] as $i => $body) {
            $res = $this->api('PATCH', $base, $t, $body);
            self::assertSame(422, $res->status(), "case $i: " . $res->body());
        }
        self::assertFalse(self::json($this->api('GET', '/api/v1/websites/' . $this->plain . '/web-policy', $t))['data']['headers_custom'], 'nothing was stored');
    }

    public function testCacheRulesAndThePageCache(): void
    {
        $t = $this->token($this->res);
        $base = '/api/v1/websites/' . $this->php . '/web-policy/cache';
        $res = $this->api('PATCH', $base, $t, ['static_ttl' => 86400, 'dynamic_mode' => 'public', 'dynamic_ttl' => 120, 'bypass_paths' => ['/members', '/cart/'], 'page_cache' => ['enabled' => true, 'ttl' => 600], 'brotli' => false]);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame([86400, 'public', 120, ['/members', '/cart'], true, 600, false, true], [$d['cache']['static_ttl'], $d['cache']['dynamic_mode'], $d['cache']['dynamic_ttl'], $d['cache']['bypass_paths'], $d['cache']['page_cache']['enabled'], $d['cache']['page_cache']['ttl'], $d['cache']['brotli'], $d['cache_custom']]);

        // Only what is sent changes.
        $d = self::json($this->api('PATCH', $base, $t, ['static_ttl' => 3600]))['data'];
        self::assertSame([3600, 'public', true], [$d['cache']['static_ttl'], $d['cache']['dynamic_mode'], $d['cache']['page_cache']['enabled']]);

        $res = $this->api('POST', $base . '/purge', $t);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame('app.test', $this->lastCall('sites.page_cache_purge')['domain']);
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'website.page_cache_purge'"));
    }

    public function testCacheInputIsValidated(): void
    {
        $t = $this->token($this->res);
        $php = '/api/v1/websites/' . $this->php . '/web-policy/cache';
        foreach ([['static_ttl' => 30], ['static_ttl' => 99999999], ['static_ttl' => '3600'], ['dynamic_mode' => 'always'], ['dynamic_mode' => 'public', 'dynamic_ttl' => 5],
            ['bypass_paths' => ['members']], ['bypass_paths' => ['/a/../b']], ['bypass_paths' => 'members'], ['page_cache' => ['enabled' => true, 'ttl' => 7]], ['page_cache' => true], ['brotli' => 1], ['colour' => 'red']] as $i => $body) {
            self::assertSame(422, $this->api('PATCH', $php, $t, $body)->status(), "case $i");
        }
        // The page cache is for PHP websites.
        $res = $this->api('PATCH', '/api/v1/websites/' . $this->plain . '/web-policy/cache', $t, ['page_cache' => ['enabled' => true]]);
        self::assertSame(422, $res->status());
        self::assertArrayHasKey('page_cache', self::json($res)['error']['fields']);
        // Purging with the page cache off is refused.
        self::assertSame(409, $this->api('POST', '/api/v1/websites/' . $this->php . '/web-policy/cache/purge', $t)->status());
    }

    public function testResetGoesBackToTheDefaultsPerSectionOrAll(): void
    {
        $t = $this->token($this->res);
        $w = '/api/v1/websites/' . $this->plain . '/web-policy';
        $this->api('PATCH', $w . '/headers', $t, ['referrer' => 'no-referrer']);
        $this->api('PATCH', $w . '/cache', $t, ['static_ttl' => 3600]);
        $d = self::json($this->api('DELETE', $w . '/headers', $t))['data'];
        self::assertSame([false, true, 'strict-origin-when-cross-origin'], [$d['headers_custom'], $d['cache_custom'], $d['headers']['referrer']]);
        $d = self::json($this->api('DELETE', $w, $t))['data'];
        self::assertSame([false, false, 0], [$d['headers_custom'], $d['cache_custom'], $d['cache']['static_ttl']]);
        self::assertSame(200, $this->api('DELETE', $w . '/cache', $t)->status());
    }

    public function testOtherAccountsWebsitesAreNotFoundAndReadOnlyTokensCannotChange(): void
    {
        $t = $this->token($this->res);
        $w = '/api/v1/websites/' . $this->other . '/web-policy';
        foreach ([['GET', ''], ['PATCH', '/headers'], ['PATCH', '/cache'], ['POST', '/cache/purge'], ['DELETE', '/headers'], ['DELETE', '/cache'], ['DELETE', '']] as [$m, $p]) {
            self::assertSame(404, $this->api($m, $w . $p, $t, $m === 'PATCH' ? ['referrer' => 'no-referrer'] : [])->status(), "$m $p");
        }
        $ro = $this->token($this->res, 'read');
        $mine = '/api/v1/websites/' . $this->plain . '/web-policy';
        self::assertSame(200, $this->api('GET', $mine, $ro)->status());
        self::assertSame(403, $this->api('PATCH', $mine . '/headers', $ro, ['referrer' => 'no-referrer'])->status());
        self::assertSame(403, $this->api('DELETE', $mine, $ro)->status());
        self::assertSame(401, $this->api('GET', $mine, null)->status());
    }
}
