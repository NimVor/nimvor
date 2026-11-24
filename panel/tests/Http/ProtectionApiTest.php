<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\UserRepository;
use NimVor\Services\ApiTokenService;
use NimVor\Services\GeoIpService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** REST API v1: a website's web protection. */
final class ProtectionApiTest extends HttpTestCase
{
    private const LIMITS = [
        ['name' => 'login', 'match' => 'exact', 'path' => '/wp-login.php', 'rate' => 5, 'unit' => 'm', 'burst' => 3, 'per_ua' => false, 'enabled' => true],
        ['name' => 'api', 'match' => 'prefix', 'path' => '/api/', 'rate' => 20, 'unit' => 's', 'burst' => 0, 'per_ua' => true, 'enabled' => false],
    ];
    private const RULES = [
        ['kind' => 'ip', 'action' => 'deny', 'value' => '203.0.113.0/24', 'note' => 'scanner'],
        ['kind' => 'ua', 'action' => 'deny', 'value' => 'BadBot', 'note' => ''],
    ];

    private Connection $db;
    private int $admin;
    private int $cust;
    private int $site;
    private int $adminSite;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        foreach ([['shop.test', $this->cust], ['blog.test', $this->admin]] as [$d, $o]) {
            $this->db->run("INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, ?, 'static', ?, ?, ?, ?, 'active', 'x', 'x')", [$d, '/home/u/' . $d, '/home/u/' . $d . '/public_html', 'u' . $o, $o]);
        }
        $this->site = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'shop.test'");
        $this->adminSite = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'blog.test'");
        $this->agent->succeed('protect.status', ['limit_status' => 503, 'limits' => self::LIMITS, 'access_status' => 404, 'access' => self::RULES, 'countries' => ['mode' => 'deny', 'codes' => ['KP']]]);
        foreach (['protect.limits', 'protect.access', 'protect.countries'] as $a) {
            $this->agent->succeed($a, []);
        }
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
        return $this->app->container->get(ApiTokenService::class)->create($userId, ['name' => 't' . bin2hex(random_bytes(2)), 'ability' => $ability, 'expires_days' => 30]);
    }

    public function testTrafficLimits(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/traffic-limits';
        $d = self::json($this->api('GET', $base, $cust))['data'];
        self::assertSame([503, ['login', 'api'], false], [$d['status'], array_column($d['limits'], 'name'), $d['limits'][1]['enabled']]);

        $res = $this->api('POST', $base, $cust, ['name' => 'Search', 'path' => 'search', 'rate' => 10, 'unit' => 's', 'burst' => 5]);
        self::assertSame(201, $res->status(), $res->body());
        self::assertSame(['search', '/search', 10], [self::json($res)['data']['name'], self::json($res)['data']['path'], self::json($res)['data']['rate']]);
        $sent = $this->lastCall('protect.limits');
        self::assertSame(['shop.test', 503, ['login', 'api', 'search']], [$sent['domain'], $sent['status'], array_column($sent['limits'], 'name')]);
        self::assertNotNull($this->db->scalar("SELECT id FROM audit_logs WHERE action = 'site_traffic.limit_save'"));

        // The same name replaces the limit.
        $res = $this->api('POST', $base, $cust, ['name' => 'login', 'match' => 'exact', 'path' => '/wp-login.php', 'rate' => 3, 'unit' => 'm']);
        self::assertSame(200, $res->status(), $res->body());
        $sent = $this->lastCall('protect.limits');
        self::assertSame([2, 3], [count($sent['limits']), $sent['limits'][0]['rate']]);

        $res = $this->api('PATCH', "$base/api", $cust, ['enabled' => true]);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame([true, true], [$this->lastCall('protect.limits')['limits'][1]['enabled'], $this->lastCall('protect.limits')['limits'][0]['enabled']]);

        $res = $this->api('DELETE', "$base/login", $cust);
        self::assertSame([200, 'login'], [$res->status(), self::json($res)['data']['deleted']]);
        self::assertSame(['api'], array_column($this->lastCall('protect.limits')['limits'], 'name'));
        self::assertSame(404, $this->api('DELETE', "$base/nope", $cust)->status());
        self::assertSame(404, $this->api('PATCH', "$base/nope", $cust, ['enabled' => true])->status());

        self::assertSame(200, $this->api('PUT', "$base/status", $cust, ['status' => 444])->status());
        self::assertSame(444, $this->lastCall('protect.limits')['status']);
    }

    public function testTrafficLimitValidationNeverReachesTheServer(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/traffic-limits';
        $calls = $this->calls('protect.limits');
        $ok = ['name' => 'x', 'path' => '/p', 'rate' => 5];
        foreach ([['name' => 'Bad Name'] + $ok, ['name' => ''] + $ok, ['match' => 'regex'] + $ok, ['path' => '/a/../b'] + $ok, ['path' => '/a b'] + $ok, ['rate' => 0] + $ok, ['rate' => 1001] + $ok, ['unit' => 'h'] + $ok, ['unit' => 'm', 'rate' => 60001] + $ok, ['burst' => 1001] + $ok] as $bad) {
            self::assertSame(422, $this->api('POST', $base, $cust, $bad)->status(), json_encode($bad));
        }
        self::assertSame(422, $this->api('PUT', "$base/status", $cust, ['status' => 200])->status());
        self::assertSame(422, $this->api('PATCH', "$base/login", $cust, ['enabled' => 'maybe'])->status());
        self::assertSame($calls, $this->calls('protect.limits'));
        // At most 20 limits.
        $many = [];
        for ($i = 0; $i < 20; $i++) {
            $many[] = ['name' => "l$i", 'match' => 'prefix', 'path' => '/x', 'rate' => 5, 'unit' => 's', 'burst' => 0, 'per_ua' => false, 'enabled' => true];
        }
        $this->agent->succeed('protect.status', ['limit_status' => 429, 'limits' => $many]);
        self::assertSame(409, $this->api('POST', $base, $cust, ['name' => 'extra'] + $ok)->status());
    }

    public function testAccessList(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/access-list';
        $d = self::json($this->api('GET', $base, $cust))['data'];
        self::assertSame([404, 200, ['203.0.113.0/24', 'BadBot']], [$d['status'], $d['max'], array_column($d['rules'], 'value')]);

        $res = $this->api('POST', $base, $cust, ['kind' => 'ip', 'action' => 'allow', 'value' => '198.51.100.7', 'note' => 'office']);
        self::assertSame(201, $res->status(), $res->body());
        $sent = $this->lastCall('protect.access');
        self::assertSame(['shop.test', 404, 3, 'allow'], [$sent['domain'], $sent['status'], count($sent['rules']), $sent['rules'][2]['action']]);
        // The same entry changes instead of doubling.
        $this->api('POST', $base, $cust, ['kind' => 'ip', 'action' => 'allow', 'value' => '203.0.113.0/24']);
        $sent = $this->lastCall('protect.access');
        self::assertSame([2, 'allow'], [count($sent['rules']), $sent['rules'][0]['action']]);

        $req = new Request('DELETE', $base, ['kind' => 'ua', 'value' => 'BadBot'], [], ['REMOTE_ADDR' => '192.0.2.50'], ['authorization' => 'Bearer ' . $cust]);
        self::assertSame(200, $this->app->handle($req)->status());
        self::assertSame(['203.0.113.0/24'], array_column($this->lastCall('protect.access')['rules'], 'value'));
        $missing = new Request('DELETE', $base, ['kind' => 'ua', 'value' => 'Nope'], [], ['REMOTE_ADDR' => '192.0.2.50'], ['authorization' => 'Bearer ' . $cust]);
        self::assertSame(404, $this->app->handle($missing)->status());
        self::assertSame(422, $this->api('DELETE', $base, $cust)->status());

        self::assertSame(200, $this->api('PUT', "$base/status", $cust, ['status' => 444])->status());
        self::assertSame(444, $this->lastCall('protect.access')['status']);

        $res = $this->api('POST', "$base/import", $cust, ['text' => "deny ip 192.0.2.0/24  # lab\ndeny url /private/", 'mode' => 'replace']);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame(['192.0.2.0/24', '/private/'], array_column($this->lastCall('protect.access')['rules'], 'value'));
        $res = $this->api('POST', "$base/import", $cust, ['text' => 'deny ip 192.0.2.99']);
        self::assertSame(3, count($this->lastCall('protect.access')['rules']), 'merge adds to the list');
    }

    public function testAccessListValidationNeverReachesTheServer(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/access-list';
        $calls = $this->calls('protect.access');
        foreach ([[], ['kind' => 'cookie', 'action' => 'deny', 'value' => 'x'], ['kind' => 'ip', 'action' => 'maybe', 'value' => '1.2.3.4'], ['kind' => 'ip', 'action' => 'deny', 'value' => 'not-an-ip'], ['kind' => 'ip', 'action' => 'deny', 'value' => ''], ['kind' => 'url', 'action' => 'deny', 'value' => 'no-slash']] as $bad) {
            self::assertSame(422, $this->api('POST', $base, $cust, $bad)->status(), json_encode($bad));
        }
        self::assertSame(422, $this->api('PUT', "$base/status", $cust, ['status' => 500])->status());
        self::assertSame(422, $this->api('POST', "$base/import", $cust, ['text' => '', 'mode' => 'merge'])->status());
        self::assertSame(422, $this->api('POST', "$base/import", $cust, ['text' => 'deny ip 1.2.3.4', 'mode' => 'sideways'])->status());
        self::assertSame($calls, $this->calls('protect.access'));
    }

    public function testCountryRestriction(): void
    {
        $cust = $this->token($this->cust);
        $url = '/api/v1/websites/' . $this->site . '/countries';
        $d = self::json($this->api('GET', $url, $cust))['data'];
        self::assertSame(['deny', ['KP'], false], [$d['mode'], $d['codes'], $d['database_loaded']]);

        // Without the address database nothing can be set.
        $calls = $this->calls('protect.countries');
        self::assertSame(409, $this->api('PUT', $url, $cust, ['mode' => 'deny', 'codes' => ['KP']])->status());
        self::assertSame($calls, $this->calls('protect.countries'));

        $csv = sys_get_temp_dir() . '/nimvor-geo-' . bin2hex(random_bytes(4)) . '.csv';
        file_put_contents($csv, "198.51.100.0,198.51.100.255,KP\n203.0.113.0,203.0.113.255,DE\n");
        $this->app->container->get(GeoIpService::class)->import($csv);
        @unlink($csv);
        self::assertTrue(self::json($this->api('GET', $url, $cust))['data']['database_loaded']);

        $res = $this->api('PUT', $url, $cust, ['mode' => 'deny', 'codes' => ['kp', 'KP', 'de']]);
        self::assertSame(200, $res->status(), $res->body());
        $sent = $this->lastCall('protect.countries');
        self::assertSame(['shop.test', 'deny', ['KP', 'DE']], [$sent['domain'], $sent['mode'], $sent['codes']]);
        self::assertNotEmpty($sent['ranges']['KP']);

        foreach ([['mode' => 'maybe'], ['mode' => 'deny'], ['mode' => 'deny', 'codes' => ['ZZZ']], ['mode' => 'deny', 'codes' => 'KP'], ['mode' => 'allow_only', 'codes' => ['XX']]] as $bad) {
            self::assertSame(422, $this->api('PUT', $url, $cust, $bad)->status(), json_encode($bad));
        }
        self::assertSame(200, $this->api('PUT', $url, $cust, ['mode' => 'off'])->status());
        self::assertSame('', $this->lastCall('protect.countries')['mode']);
    }

    public function testScopeAndPermissions(): void
    {
        $cust = $this->token($this->cust);
        $ro = $this->token($this->cust, 'read');
        $mine = '/api/v1/websites/' . $this->site;
        $other = '/api/v1/websites/' . $this->adminSite;
        $ok = ['name' => 'x', 'path' => '/p', 'rate' => 5];
        $rule = ['kind' => 'ip', 'action' => 'deny', 'value' => '1.2.3.4'];
        $writes = [['POST', '/traffic-limits', $ok], ['PUT', '/traffic-limits/status', ['status' => 429]], ['PATCH', '/traffic-limits/login', ['enabled' => false]], ['DELETE', '/traffic-limits/login', []], ['POST', '/access-list', $rule], ['DELETE', '/access-list', []], ['PUT', '/access-list/status', ['status' => 403]], ['POST', '/access-list/import', ['text' => 'deny ip 1.2.3.4']], ['PUT', '/countries', ['mode' => 'off']]];
        $count = fn (): int => $this->calls('protect.limits') + $this->calls('protect.access') + $this->calls('protect.countries');
        $n = $count();
        foreach ($writes as [$m, $p, $b]) {
            self::assertSame(403, $this->api($m, $mine . $p, $ro, $b)->status(), "read-only $m $p");
            self::assertSame(404, $this->api($m, $other . $p, $cust, $b)->status(), "other website $m $p");
        }
        foreach (['/traffic-limits', '/access-list', '/countries'] as $p) {
            self::assertSame(200, $this->api('GET', $mine . $p, $ro)->status(), $p);
            self::assertSame(404, $this->api('GET', $other . $p, $cust)->status(), $p);
        }
        self::assertSame($n, $count(), 'nothing reaches the server');
        self::assertSame(401, $this->api('GET', $mine . '/access-list', null)->status());
    }
}
