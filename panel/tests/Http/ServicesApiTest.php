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

/** REST API v1: the server's services. */
final class ServicesApiTest extends HttpTestCase
{
    private Connection $db;
    private int $admin;
    private int $cust;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('services.status', [
            ['unit' => 'nginx.service', 'description' => 'Web server', 'active_state' => 'active', 'sub_state' => 'running', 'unit_file_state' => 'enabled'],
            ['unit' => 'cron.service', 'active_state' => 'inactive', 'sub_state' => 'dead', 'unit_file_state' => 'disabled'],
        ]);
        $this->agent->succeed('nimvor.services', ['services' => [['key' => 'agent', 'unit' => 'nimvor-agent.service', 'active_state' => 'active']]]);
        $this->agent->succeed('services.control', ['ok' => true]);
        $this->agent->succeed('nimvor.restart', ['unit' => 'nimvor-scheduler.service']);
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

    public function testListTheServices(): void
    {
        $admin = $this->token($this->admin);
        $d = self::json($this->api('GET', '/api/v1/services', $admin))['data'];
        self::assertSame(['nginx.service', 'cron.service'], array_column($d, 'unit'));
        self::assertSame(['active', 'running', 'enabled', 'Web server'], [$d[0]['active_state'], $d[0]['sub_state'], $d[0]['enabled'], $d[0]['description']]);
        self::assertSame('nimvor-agent.service', self::json($this->api('GET', '/api/v1/services/nimvor', $admin))['data']['services'][0]['unit']);
    }

    public function testControlAServiceIsAuditedAndValidated(): void
    {
        $admin = $this->token($this->admin);
        $res = $this->api('POST', '/api/v1/services/control', $admin, ['unit' => 'php8.3-fpm.service', 'op' => 'reload']);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame([false, 'Reload of php8.3-fpm.service completed.'], [self::json($res)['data']['scheduled'], self::json($res)['data']['message']]);
        self::assertSame(['php8.3-fpm.service', 'reload'], [$this->lastCall('services.control')['unit'], $this->lastCall('services.control')['op']]);
        self::assertNotNull($this->db->scalar("SELECT id FROM audit_logs WHERE action = 'service.reload'"));

        $this->agent->succeed('services.control', ['scheduled' => true]);
        $res = $this->api('POST', '/api/v1/services/control', $admin, ['unit' => 'nginx.service', 'op' => 'restart']);
        self::assertSame([202, true], [$res->status(), self::json($res)['data']['scheduled']]);

        $calls = $this->calls('services.control');
        foreach ([[], ['unit' => 'nginx', 'op' => 'restart'], ['unit' => 'nginx.service', 'op' => 'explode'], ['unit' => '../etc/passwd.service', 'op' => 'stop'], ['unit' => "a b.service", 'op' => 'stop'], ['op' => 'stop']] as $bad) {
            self::assertSame(422, $this->api('POST', '/api/v1/services/control', $admin, $bad)->status(), json_encode($bad));
        }
        self::assertSame($calls, $this->calls('services.control'), 'nothing invalid reaches the server');
    }

    public function testTheAgentsAllowlistAndFailuresAreReported(): void
    {
        $admin = $this->token($this->admin);
        $this->agent->fail('services.control', 403, 'forbidden', 'This service is not managed by NimVor.');
        $res = $this->api('POST', '/api/v1/services/control', $admin, ['unit' => 'ssh.service', 'op' => 'stop']);
        self::assertSame(502, $res->status());
        self::assertSame('forbidden', self::json($res)['error']['code']);
        self::assertNotNull($this->db->scalar("SELECT id FROM audit_logs WHERE action = 'service.stop' AND result = 'failure'"), 'the failure is audited');
    }

    public function testRestartNimvorServices(): void
    {
        $admin = $this->token($this->admin);
        $res = $this->api('POST', '/api/v1/services/nimvor/scheduler/restart', $admin);
        self::assertSame([200, 'nimvor-scheduler.service'], [$res->status(), self::json($res)['data']['unit']]);
        self::assertSame('scheduler', $this->lastCall('nimvor.restart')['key']);
        $this->agent->succeed('nimvor.restart', ['unit' => 'nimvor-panel.service', 'scheduled' => true]);
        self::assertSame(202, $this->api('POST', '/api/v1/services/nimvor/panel/restart', $admin)->status());
        $calls = $this->calls('nimvor.restart');
        self::assertSame(422, $this->api('POST', '/api/v1/services/nimvor/Panel1/restart', $admin)->status());
        self::assertSame($calls, $this->calls('nimvor.restart'));
    }

    public function testCustomersAndReadOnlyTokensAreRefused(): void
    {
        $cust = $this->token($this->cust);
        $ro = $this->token($this->admin, 'read');
        foreach (['/api/v1/services', '/api/v1/services/nimvor'] as $p) {
            self::assertSame(403, $this->api('GET', $p, $cust)->status(), $p);
            self::assertSame(200, $this->api('GET', $p, $ro)->status(), $p);
        }
        $n = $this->calls('services.control') + $this->calls('nimvor.restart');
        foreach ([['/services/control', ['unit' => 'nginx.service', 'op' => 'stop']], ['/services/nimvor/agent/restart', []]] as [$p, $b]) {
            self::assertSame(403, $this->api('POST', '/api/v1' . $p, $cust, $b)->status(), "$p customer");
            self::assertSame(403, $this->api('POST', '/api/v1' . $p, $ro, $b)->status(), "$p read-only");
        }
        self::assertSame($n, $this->calls('services.control') + $this->calls('nimvor.restart'));
        self::assertSame(401, $this->api('GET', '/api/v1/services', null)->status());
    }
}
