<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\UserRepository;
use NimVor\Services\ApiTokenService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** REST API v1: a website's background workers. */
final class WorkersApiTest extends HttpTestCase
{
    private Connection $db;
    private int $admin;
    private int $cust;
    private int $site;
    private int $adminSite;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'reseller');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->site = $sites->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $users->find($this->cust));
        $this->adminSite = $sites->create(['domain' => 'other.test', 'kind' => 'static'], (array) $users->find($this->admin));
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('workers.apply', ['ok' => true]);
        $this->agent->succeed('workers.restart', ['ok' => true]);
        $this->agent->succeed('workers.remove', ['ok' => true]);
        $this->agent->succeed('workers.status', [['unit' => 'nimvor-worker-shop-queue.service', 'active_state' => 'active', 'sub_state' => 'running', 'running' => true, 'procs_total' => 2, 'procs_running' => 2, 'memory_bytes' => 1048576]]);
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

    /** @return array<string, mixed> */
    private function create(string $token, array $in = []): array
    {
        $res = $this->api('POST', '/api/v1/websites/' . $this->site . '/workers', $token, $in + ['name' => 'Queue', 'command' => 'php artisan queue:work', 'procs' => 2, 'memory_mb' => 512, 'env' => ['QUEUE' => 'default', 'API_TOKEN' => 'super-secret-value']]);
        self::assertSame(201, $res->status(), $res->body());
        return self::json($res)['data'];
    }

    public function testCreateReadChangeRestartAndDelete(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/workers';
        $w = $this->create($cust);
        self::assertSame(['Queue', 'nimvor-worker-shop-queue.service', 2, 512, true, ['QUEUE', 'API_TOKEN']], [$w['name'], $w['unit'], $w['procs'], $w['memory_mb'], $w['enabled'], $w['env_keys']]);
        self::assertStringNotContainsString('super-secret-value', (string) json_encode($w), 'the values of the variables are never returned');
        self::assertSame(['active', true, 2], [$w['state']['active'], $w['state']['running'], $w['state']['procs_running']]);
        $sent = $this->lastCall('workers.apply');
        self::assertSame(['shop.test', 'php artisan queue:work', 'default', 'super-secret-value', true, 2], [$sent['domain'], $sent['command'], $sent['env']['QUEUE'], $sent['env']['API_TOKEN'], $sent['enabled'], $sent['procs']]);
        $audit = (string) $this->db->scalar("SELECT group_concat(details) FROM audit_logs WHERE action = 'worker.create'");
        self::assertStringNotContainsString('super-secret-value', $audit);

        self::assertSame([$w['id']], array_column(self::json($this->api('GET', $base, $cust))['data'], 'id'));
        self::assertSame('Queue', self::json($this->api('GET', "$base/{$w['id']}", $cust))['data']['name']);

        // PATCH changes only what is sent; the variables stay.
        $res = $this->api('PATCH', "$base/{$w['id']}", $cust, ['procs' => 4]);
        self::assertSame(200, $res->status(), $res->body());
        $sent = $this->lastCall('workers.apply');
        self::assertSame([4, 'php artisan queue:work', 512, 'super-secret-value'], [$sent['procs'], $sent['command'], $sent['memory_mb'], $sent['env']['API_TOKEN']]);
        $res = $this->api('PATCH', "$base/{$w['id']}", $cust, ['enabled' => false]);
        self::assertFalse(self::json($res)['data']['enabled']);
        self::assertFalse($this->lastCall('workers.apply')['enabled']);
        // The variables are replaced as a set.
        $this->api('PATCH', "$base/{$w['id']}", $cust, ['env' => ['ONLY' => 'one']]);
        self::assertSame(['ONLY' => 'one'], (array) $this->lastCall('workers.apply')['env']);

        $res = $this->api('POST', "$base/{$w['id']}/restart", $cust);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame('shop-queue', $this->lastCall('workers.restart')['slug']);

        $res = $this->api('DELETE', "$base/{$w['id']}", $cust);
        self::assertSame([200, $w['id']], [$res->status(), self::json($res)['data']['deleted']]);
        self::assertSame('shop-queue', $this->lastCall('workers.remove')['slug']);
        self::assertSame(404, $this->api('GET', "$base/{$w['id']}", $cust)->status());
        self::assertSame(404, $this->api('DELETE', "$base/{$w['id']}", $cust)->status());
    }

    public function testValidationNeverReachesTheServer(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/workers';
        $calls = $this->calls('workers.apply');
        $ok = ['name' => 'W', 'command' => 'node app.js'];
        foreach ([
            [['name' => ''] + $ok, 'name'], [['command' => ''] + $ok, 'command'], [['command' => "ls\nrm -rf /"] + $ok, 'command'],
            [['procs' => 0] + $ok, 'procs'], [['procs' => 21] + $ok, 'procs'], [['restart' => 'sometimes'] + $ok, 'restart'],
            [['memory_mb' => -1] + $ok, 'memory_mb'], [['directory' => '../etc'] + $ok, 'directory'], [['runtime' => 'ruby:3'] + $ok, 'runtime'],
            [['env' => 'A=b'] + $ok, 'env'], [['env' => ['lower' => 'x']] + $ok, 'env'], [['env' => ['A' => "x\ny"]] + $ok, 'env'],
        ] as [$body, $field]) {
            $res = $this->api('POST', $base, $cust, $body);
            self::assertSame(422, $res->status(), json_encode($body) . $res->body());
            self::assertArrayHasKey($field, self::json($res)['error']['fields'], json_encode($body));
        }
        self::assertSame($calls, $this->calls('workers.apply'));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM workers'));
    }

    public function testTheServerRefusingTheWorkerUndoesTheChange(): void
    {
        $cust = $this->token($this->cust);
        $w = $this->create($cust);
        $this->agent->fail('workers.apply', 500, 'failed', 'unit check failed');
        $res = $this->api('PATCH', '/api/v1/websites/' . $this->site . '/workers/' . $w['id'], $cust, ['command' => 'broken']);
        self::assertSame(502, $res->status());
        self::assertSame('php artisan queue:work', (string) $this->db->scalar('SELECT command FROM workers WHERE id = ' . $w['id']), 'the previous command is back');
        $res = $this->api('POST', '/api/v1/websites/' . $this->site . '/workers', $cust, ['name' => 'Second', 'command' => 'x']);
        self::assertSame(502, $res->status());
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM workers'), 'the new worker was not kept');
    }

    public function testScopeAndPermissions(): void
    {
        $cust = $this->token($this->cust);
        $ro = $this->token($this->cust, 'read');
        $w = $this->create($cust);
        $base = '/api/v1/websites/' . $this->site . '/workers';
        self::assertSame(200, $this->api('GET', $base, $ro)->status());
        self::assertSame(200, $this->api('GET', "$base/{$w['id']}", $ro)->status());
        $n = $this->calls('workers.apply') + $this->calls('workers.restart') + $this->calls('workers.remove');
        foreach ([['POST', '', ['name' => 'x', 'command' => 'y']], ['PATCH', "/{$w['id']}", ['procs' => 3]], ['DELETE', "/{$w['id']}", []], ['POST', "/{$w['id']}/restart", []]] as [$m, $p, $b]) {
            self::assertSame(403, $this->api($m, $base . $p, $ro, $b)->status(), "$m $p");
        }
        // The other customer's website, and this worker through it.
        $other = '/api/v1/websites/' . $this->adminSite . '/workers';
        foreach ([['GET', '', []], ['POST', '', ['name' => 'x', 'command' => 'y']], ['GET', "/{$w['id']}", []], ['PATCH', "/{$w['id']}", ['procs' => 3]], ['DELETE', "/{$w['id']}", []], ['POST', "/{$w['id']}/restart", []]] as [$m, $p, $b]) {
            self::assertSame(404, $this->api($m, $other . $p, $cust, $b)->status(), "$m $p");
        }
        self::assertSame($n, $this->calls('workers.apply') + $this->calls('workers.restart') + $this->calls('workers.remove'));
        self::assertSame(401, $this->api('GET', $base, null)->status());
        self::assertSame(200, $this->api('GET', $other, $this->token($this->admin))->status());
    }
}
