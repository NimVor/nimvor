<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\ApiTokenService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** REST API v1: website settings, re-apply, permissions, environment variables and runtime. */
final class SiteSettingsApiTest extends HttpTestCase
{
    private Connection $db;
    private int $admin;
    private int $cust;
    private int $site;
    private int $adminSite;
    private int $nodeSite;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'reseller');
        $sites = $this->app->container->get(WebsiteService::class);
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->agent->succeed('workers.apply', ['ok' => true]);
        $this->agent->succeed('runtime.status', ['arch' => 'x64', 'node' => [['version' => '20.1.0'], ['version' => '22.0.0']], 'python' => [['version' => '3.12.1']], 'system' => []]);
        $this->agent->succeed('sites.permissions', ['checked' => 120, 'wrong' => 3, 'fixed' => 3, 'skipped_count' => 1]);
        $this->site = $sites->create(['domain' => 'shop.test', 'kind' => 'static', 'owner_id' => (string) $this->cust], ['role_scope' => 'all', 'id' => 1]);
        $this->adminSite = $sites->create(['domain' => 'other.test', 'kind' => 'static', 'owner_id' => (string) $this->admin], ['role_scope' => 'all', 'id' => 1]);
        $this->nodeSite = $sites->create(['domain' => 'app.test', 'kind' => 'nodejs', 'app_runtime' => 'node:20.1.0', 'app_port' => '3000', 'app_command' => 'node server.js', 'owner_id' => (string) $this->cust], ['role_scope' => 'all', 'id' => 1]);
        $this->db = $this->app->container->get(Connection::class);
        $node = (array) $this->db->fetch('SELECT * FROM websites WHERE id = ?', [$this->nodeSite]);
        $this->app->container->get(\NimVor\Services\WorkerService::class)->save($node + ['owner_id' => $this->cust], null, ['name' => 'app', 'directory' => '.', 'command' => 'node server.js', 'runtime' => 'node:20.1.0', 'restart' => 'always', 'memory_mb' => 256, 'procs' => 1, 'enabled' => '1']);
    }

    /** @param array<string, mixed> $json */
    private function api(string $method, string $path, ?string $token, array $json = []): Response
    {
        $h = ['content-type' => 'application/json'] + ($token === null ? [] : ['authorization' => 'Bearer ' . $token]);
        return $this->app->handle(new Request($method, $path, [], [], ['REMOTE_ADDR' => '192.0.2.50'], $h, $json === [] ? '' : (string) json_encode($json)));
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

    public function testReadAndChangeSettingsKeepingWhatIsNotSent(): void
    {
        $t = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/settings';
        $d = self::json($this->api('GET', $base, $t))['data'];
        self::assertSame(['shop.test', [], true, false], [$d['domain'], $d['aliases'], $d['monitoring'], $d['directory_listing']]);

        $res = $this->api('PATCH', $base, $t, ['aliases' => ['www.shop.test', 'shop.example'], 'client_max_body_mb' => 128]);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame([['www.shop.test', 'shop.example'], 128], [$d['aliases'], $d['client_max_body_mb']]);

        // Only the toggles: the aliases and the limit stay.
        $res = $this->api('PATCH', $base, $t, ['directory_listing' => true, 'monitoring' => false]);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame([true, false, ['www.shop.test', 'shop.example'], 128], [$d['directory_listing'], $d['monitoring'], $d['aliases'], $d['client_max_body_mb']]);
        self::assertSame(1, (int) $this->db->scalar('SELECT monitor_disabled FROM websites WHERE id = ?', [$this->site]));

        // An empty list removes the aliases.
        $d = self::json($this->api('PATCH', $base, $t, ['aliases' => []]))['data'];
        self::assertSame([], $d['aliases']);
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM audit_logs WHERE action = ?', ['website.monitoring_off']));
    }

    public function testBadInputIsRefusedWithTheFieldsAndNothingIsChanged(): void
    {
        $t = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/settings';
        $res = $this->api('PATCH', $base, $t, ['aliases' => ['not a domain'], 'client_max_body_mb' => 99999, 'colour' => 'red', 'monitoring' => 'yes']);
        self::assertSame(422, $res->status());
        $f = self::json($res)['error']['fields'];
        self::assertArrayHasKey('colour', $f);
        self::assertArrayHasKey('monitoring', $f);

        $res = $this->api('PATCH', $base, $t, ['document_root_sub' => '../etc']);
        self::assertSame(422, $res->status());
        self::assertArrayHasKey('document_root_sub', self::json($res)['error']['fields']);

        // A name that belongs to another website.
        $res = $this->api('PATCH', $base, $t, ['aliases' => ['other.test']]);
        self::assertSame(422, $res->status(), $res->body());
        self::assertSame([], self::json($this->api('GET', $base, $t))['data']['aliases']);
    }

    public function testAnotherAccountsWebsiteIsNotFoundAndReadOnlyTokensCannotChange(): void
    {
        $t = $this->token($this->cust);
        foreach ([['GET', 'settings'], ['PATCH', 'settings'], ['POST', 'reapply'], ['GET', 'permissions'], ['POST', 'permissions/repair'], ['GET', 'env'], ['PATCH', 'env'], ['GET', 'runtime'], ['PATCH', 'runtime']] as [$m, $p]) {
            self::assertSame(404, $this->api($m, '/api/v1/websites/' . $this->adminSite . '/' . $p, $t, $m === 'PATCH' ? ['monitoring' => true] : [])->status(), "$m $p");
        }
        $ro = $this->token($this->cust, 'read');
        self::assertSame(200, $this->api('GET', '/api/v1/websites/' . $this->site . '/settings', $ro)->status());
        self::assertSame(403, $this->api('PATCH', '/api/v1/websites/' . $this->site . '/settings', $ro, ['monitoring' => false])->status());
        self::assertSame(403, $this->api('POST', '/api/v1/websites/' . $this->site . '/reapply', $ro)->status());
        self::assertSame(401, $this->api('GET', '/api/v1/websites/' . $this->site . '/settings', null)->status());
    }

    public function testReapplyAndThePermissionCheckAndRepair(): void
    {
        $t = $this->token($this->cust);
        $res = $this->api('POST', '/api/v1/websites/' . $this->site . '/reapply', $t);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame('shop.test', self::json($res)['data']['reapplied']);

        $res = $this->api('GET', '/api/v1/websites/' . $this->site . '/permissions', $t);
        $d = self::json($res)['data'];
        self::assertSame([false, 120, 1], [$d['repaired'], $d['checked'], $d['skipped']]);
        self::assertTrue($this->lastCall('sites.permissions')['dry_run'], 'a check changes nothing');

        $d = self::json($this->api('POST', '/api/v1/websites/' . $this->site . '/permissions/repair', $t))['data'];
        self::assertSame([true, 3], [$d['repaired'], $d['fixed']]);
        self::assertFalse($this->lastCall('sites.permissions')['dry_run']);
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM audit_logs WHERE action = ?', ['website.permissions_repair']));
    }

    public function testEnvironmentVariablesAreWriteOnly(): void
    {
        $t = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/env';
        self::assertSame(['names' => [], 'count' => 0], array_intersect_key(self::json($this->api('GET', $base, $t))['data'], ['names' => 1, 'count' => 1]));

        $res = $this->api('PATCH', $base, $t, ['set' => ['STRIPE_KEY' => 'sk_live_abc', 'APP_ENV' => 'production']]);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame(['APP_ENV', 'STRIPE_KEY'], self::json($res)['data']['names']);
        self::assertStringNotContainsString('sk_live_abc', $res->body(), 'values are never returned');
        self::assertSame(['STRIPE_KEY' => 'sk_live_abc', 'APP_ENV' => 'production'], $this->lastCall('sites.apply')['env']);

        // Change one, remove one, keep the rest.
        $res = $this->api('PATCH', $base, $t, ['set' => ['APP_ENV' => 'staging'], 'unset' => ['STRIPE_KEY']]);
        self::assertSame(['APP_ENV'], self::json($res)['data']['names']);
        self::assertSame(['APP_ENV' => 'staging'], $this->lastCall('sites.apply')['env']);
        self::assertStringNotContainsString('staging', (string) $this->db->scalar("SELECT details FROM audit_logs WHERE action LIKE 'website.env%' ORDER BY id DESC LIMIT 1"));
    }

    public function testEnvironmentVariablesAreValidated(): void
    {
        $t = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/env';
        foreach ([['set' => ['bad name' => 'x']], ['set' => ['PATH' => '/tmp']], ['set' => ['A' => ['x']]], ['set' => ['A', 'B']], ['unset' => 'A'], []] as $i => $body) {
            $res = $this->api('PATCH', $base, $t, $body);
            self::assertSame(422, $res->status(), "case $i: " . $res->body());
        }
        self::assertSame(0, self::json($this->api('GET', $base, $t))['data']['count'], 'nothing was stored');
    }

    public function testRuntimeOfANodeWebsiteAndRefusalForOthers(): void
    {
        $t = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->nodeSite . '/runtime';
        $d = self::json($this->api('GET', $base, $t))['data'];
        self::assertSame(['nodejs', 'node:20.1.0'], [$d['type'], $d['app_runtime']]);
        self::assertContains('node:22.0.0', $d['available']);

        $res = $this->api('PATCH', $base, $t, ['app_runtime' => 'node:22.0.0']);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame('node:22.0.0', self::json($res)['data']['app_runtime']);
        self::assertSame('node:22.0.0', (string) $this->db->scalar('SELECT runtime FROM workers WHERE website_id = ? AND name = ?', [$this->nodeSite, 'app']));

        self::assertSame(422, $this->api('PATCH', $base, $t, ['app_runtime' => 'python:3.12.1'])->status());
        self::assertSame(409, $this->api('GET', '/api/v1/websites/' . $this->site . '/runtime', $t)->status());
    }
}
