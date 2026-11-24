<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\ApiTokenService;
use NimVor\Tests\Support\HttpTestCase;

/** REST API v1: the server-wide allow/deny list. */
final class ServerAccessListApiTest extends HttpTestCase
{
    private const RULES = [
        ['kind' => 'ip', 'action' => 'deny', 'value' => '203.0.113.0/24', 'note' => 'scanner'],
        ['kind' => 'ua', 'action' => 'deny', 'value' => 'scrapy', 'note' => ''],
    ];

    private Connection $db;
    private int $admin;
    private int $cust;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('protect.access_global_status', ['access_status' => 404, 'access' => self::RULES]);
        $this->agent->succeed('protect.access_global', ['skipped' => []]);
    }

    /** @param array<string, mixed> $json @param array<string, string> $query */
    private function api(string $method, string $path, ?string $token, array $json = [], array $query = []): Response
    {
        $h = ['content-type' => 'application/json'] + ($token === null ? [] : ['authorization' => 'Bearer ' . $token]);
        return $this->app->handle(new Request($method, '/api/v1/security/access-list' . $path, $query, [], ['REMOTE_ADDR' => '192.0.2.50'], $h, $json === [] ? '' : (string) json_encode($json)));
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

    public function testReadAndExport(): void
    {
        $adm = $this->token($this->admin);
        $d = self::json($this->api('GET', '', $adm))['data'];
        self::assertSame([404, 1000, ['203.0.113.0/24', 'scrapy']], [$d['status'], $d['max'], array_column($d['rules'], 'value')]);
        self::assertSame("deny ip 203.0.113.0/24  # scanner\ndeny ua scrapy\n", self::json($this->api('GET', '/export', $adm))['data']['text']);
    }

    public function testChanges(): void
    {
        $adm = $this->token($this->admin);
        $res = $this->api('POST', '', $adm, ['kind' => 'ip', 'action' => 'allow', 'value' => '198.51.100.7', 'note' => 'office']);
        self::assertSame(201, $res->status(), $res->body());
        $sent = $this->lastCall('protect.access_global');
        self::assertSame([404, 3, 'allow'], [$sent['status'], count($sent['rules']), $sent['rules'][2]['action']]);
        self::assertNotNull($this->db->scalar("SELECT id FROM audit_logs WHERE action = 'access_list.add'"));
        // The same entry changes instead of doubling.
        $this->api('POST', '', $adm, ['kind' => 'ip', 'action' => 'allow', 'value' => '203.0.113.0/24']);
        $sent = $this->lastCall('protect.access_global');
        self::assertSame([2, 'allow'], [count($sent['rules']), $sent['rules'][0]['action']]);

        self::assertSame(200, $this->api('DELETE', '', $adm, [], ['kind' => 'ua', 'value' => 'scrapy'])->status());
        self::assertSame(['203.0.113.0/24'], array_column($this->lastCall('protect.access_global')['rules'], 'value'));
        self::assertSame(404, $this->api('DELETE', '', $adm, [], ['kind' => 'ua', 'value' => 'nope'])->status());
        self::assertSame(422, $this->api('DELETE', '', $adm)->status());

        self::assertSame(200, $this->api('PUT', '/status', $adm, ['status' => 444])->status());
        self::assertSame(444, $this->lastCall('protect.access_global')['status']);

        $res = $this->api('POST', '/import', $adm, ['text' => "deny ip 192.0.2.0/24  # lab\ndeny url /private/", 'mode' => 'replace']);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame(['192.0.2.0/24', '/private/'], array_column($this->lastCall('protect.access_global')['rules'], 'value'));
        $this->api('POST', '/import', $adm, ['text' => 'deny ip 192.0.2.99']);
        self::assertSame(3, count($this->lastCall('protect.access_global')['rules']), 'merge adds to the list');
    }

    public function testValidationNeverReachesTheServer(): void
    {
        $adm = $this->token($this->admin);
        $calls = $this->calls('protect.access_global');
        foreach ([[], ['kind' => 'cookie', 'action' => 'deny', 'value' => 'x'], ['kind' => 'ip', 'action' => 'maybe', 'value' => '1.2.3.4'], ['kind' => 'ip', 'action' => 'deny', 'value' => 'not-an-ip'], ['kind' => 'url', 'action' => 'deny', 'value' => 'no-slash']] as $bad) {
            self::assertSame(422, $this->api('POST', '', $adm, $bad)->status(), json_encode($bad));
        }
        self::assertSame(422, $this->api('PUT', '/status', $adm, ['status' => 500])->status());
        self::assertSame(422, $this->api('POST', '/import', $adm, ['text' => '', 'mode' => 'merge'])->status());
        self::assertSame(422, $this->api('POST', '/import', $adm, ['text' => 'deny ip 1.2.3.4', 'mode' => 'sideways'])->status());
        self::assertSame($calls, $this->calls('protect.access_global'));

        // The server-wide list holds at most 1000 entries.
        $many = [];
        for ($i = 0; $i < 1000; $i++) {
            $many[] = ['kind' => 'ip', 'action' => 'deny', 'value' => '10.' . intdiv($i, 250) . '.' . ($i % 250) . '.1', 'note' => ''];
        }
        $this->agent->succeed('protect.access_global_status', ['access_status' => 403, 'access' => $many]);
        self::assertSame(409, $this->api('POST', '', $adm, ['kind' => 'ip', 'action' => 'deny', 'value' => '192.0.2.200'])->status());
        self::assertSame($calls, $this->calls('protect.access_global'));
    }

    public function testOnlyAdministratorsAndWriteTokens(): void
    {
        $cust = $this->token($this->cust);
        $calls = $this->calls('protect.access_global');
        foreach ([['GET', ''], ['GET', '/export'], ['POST', ''], ['DELETE', ''], ['PUT', '/status'], ['POST', '/import']] as [$m, $p]) {
            self::assertSame(403, $this->api($m, $p, $cust, $m === 'GET' ? [] : ['kind' => 'ip', 'action' => 'deny', 'value' => '1.2.3.4', 'status' => 403, 'text' => 'deny ip 1.2.3.4'])->status(), "$m $p");
        }
        self::assertSame(401, $this->api('GET', '', null)->status());
        self::assertSame(403, $this->api('POST', '', $this->token($this->admin, 'read'), ['kind' => 'ip', 'action' => 'deny', 'value' => '1.2.3.4'])->status());
        self::assertSame($calls, $this->calls('protect.access_global'));
    }
}
