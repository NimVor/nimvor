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

/** REST API v1: the firewall. */
final class FirewallApiTest extends HttpTestCase
{
    private Connection $db;
    private int $admin;
    private int $cust;
    /** @var array<string, mixed> */
    private array $pending = [];

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'reseller');
        $this->db = $this->app->container->get(Connection::class);
        $this->pending = ['id' => 'abc123abc123', 'kind' => 'firewall', 'summary' => 'firewall: allow proto tcp port 8080', 'deadline' => '2026-11-24T10:02:00Z'];
        $this->agent->succeed('firewall.status', ['installed' => true, 'active' => true, 'backend' => 'ufw', 'default_incoming' => 'deny', 'default_outgoing' => 'allow', 'rules' => [
            ['spec' => 'allow 8443/tcp', 'action' => 'allow', 'port' => '8443', 'proto' => 'tcp', 'protected' => 'NimVor panel (port 8443)'],
            ['spec' => 'allow 22/tcp', 'action' => 'allow', 'port' => '22', 'proto' => 'tcp', 'ssh' => true],
        ], 'ssh_port' => 22, 'ssh_allowed' => true]);
        $this->agent->succeed('security.pending', ['pending' => null]);
        foreach (['firewall.rule_add', 'firewall.rule_delete', 'firewall.set_enabled', 'firewall.protection_set'] as $a) {
            $this->agent->succeed($a, ['changed' => true, 'pending' => $this->pending]);
        }
        $this->agent->succeed('firewall.protection', ['available' => true, 'active' => true, 'config' => ['enabled' => true, 'syn_rate' => 80], 'dropped' => ['syn' => 5]]);
        $this->agent->succeed('security.confirm', ['confirmed' => 'abc123abc123']);
        $this->agent->succeed('security.undo', ['undone' => 'abc123abc123']);
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

    public function testReadTheFirewall(): void
    {
        $admin = $this->token($this->admin);
        $res = $this->api('GET', '/api/v1/security/firewall', $admin);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame([true, true, 'ufw', 22, true, null], [$d['installed'], $d['active'], $d['backend'], $d['ssh_port'], $d['ssh_allowed'], $d['pending']]);
        self::assertSame(['allow 8443/tcp', 'allow 22/tcp'], array_column($d['rules'], 'spec'));
        self::assertSame('NimVor panel (port 8443)', $d['rules'][0]['protected']);
        self::assertNull($d['rules'][1]['protected']);

        $this->agent->succeed('security.pending', ['pending' => $this->pending]);
        self::assertSame('abc123abc123', self::json($this->api('GET', '/api/v1/security/firewall', $admin))['data']['pending']['id']);
        self::assertSame('abc123abc123', self::json($this->api('GET', '/api/v1/security/pending', $admin))['data']['pending']['id']);
        self::assertSame(['syn' => 5], self::json($this->api('GET', '/api/v1/security/firewall/protection', $admin))['data']['dropped']);
    }

    public function testAChangeWaitsForConfirmation(): void
    {
        $admin = $this->token($this->admin);
        $this->agent->succeed('security.pending', ['pending' => $this->pending]);
        $res = $this->api('POST', '/api/v1/security/firewall/rules', $admin, ['action' => 'allow', 'port' => '8080', 'proto' => 'tcp', 'comment' => 'app']);
        self::assertSame(202, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame('abc123abc123', $d['pending']['id']);
        self::assertStringContainsString('Confirm it within 2 minutes', $d['message']);
        self::assertSame(['action' => 'allow', 'port' => '8080', 'proto' => 'tcp', 'from' => '', 'comment' => 'app'], $this->lastCall('firewall.rule_add'));
        self::assertNotNull($this->db->scalar("SELECT id FROM audit_logs WHERE action = 'firewall.rule_add'"));

        $res = $this->api('POST', '/api/v1/security/confirm', $admin, ['id' => 'abc123abc123']);
        self::assertSame([200, 'abc123abc123'], [$res->status(), self::json($res)['data']['confirmed']]);
        self::assertSame('abc123abc123', $this->lastCall('security.confirm')['id']);
        $res = $this->api('POST', '/api/v1/security/undo', $admin, ['id' => 'abc123abc123']);
        self::assertSame([200, 'abc123abc123'], [$res->status(), self::json($res)['data']['undone']]);

        // Only a well-formed id goes to the agent.
        $calls = $this->calls('security.confirm');
        foreach ([[], ['id' => ''], ['id' => '../x'], ['id' => 'ZZ']] as $bad) {
            self::assertSame(422, $this->api('POST', '/api/v1/security/confirm', $admin, $bad)->status(), json_encode($bad));
        }
        self::assertSame($calls, $this->calls('security.confirm'));
    }

    public function testRulesAreValidatedAndDeletedBySpec(): void
    {
        $admin = $this->token($this->admin);
        $calls = $this->calls('firewall.rule_add');
        foreach ([['port' => ''], ['action' => 'drop-everything', 'port' => '80'], []] as $bad) {
            $res = $this->api('POST', '/api/v1/security/firewall/rules', $admin, $bad);
            self::assertSame(422, $res->status(), json_encode($bad));
        }
        self::assertSame($calls, $this->calls('firewall.rule_add'));

        $req = new Request('DELETE', '/api/v1/security/firewall/rules', ['spec' => 'allow 8080/tcp'], [], ['REMOTE_ADDR' => '192.0.2.50'], ['authorization' => 'Bearer ' . $admin]);
        self::assertSame(202, $this->app->handle($req)->status());
        self::assertSame('allow 8080/tcp', $this->lastCall('firewall.rule_delete')['spec']);
        $deletes = $this->calls('firewall.rule_delete');
        self::assertSame(422, $this->api('DELETE', '/api/v1/security/firewall/rules', $admin)->status(), 'a rule must be named');
        self::assertSame($deletes, $this->calls('firewall.rule_delete'));
    }

    public function testTheAgentsLockoutProtectionIsReported(): void
    {
        $admin = $this->token($this->admin);
        $this->agent->fail('firewall.rule_delete', 409, 'conflict', 'The NimVor panel port cannot be removed from the firewall.');
        $req = new Request('DELETE', '/api/v1/security/firewall/rules', ['spec' => 'allow 8443/tcp'], [], ['REMOTE_ADDR' => '192.0.2.50'], ['authorization' => 'Bearer ' . $admin]);
        $res = $this->app->handle($req);
        self::assertSame(502, $res->status());
        self::assertStringContainsString('cannot be removed', (string) self::json($res)['error']['message']);
    }

    public function testEnableAndProtection(): void
    {
        $admin = $this->token($this->admin);
        self::assertSame(422, $this->api('PUT', '/api/v1/security/firewall', $admin, [])->status());
        self::assertSame(422, $this->api('PUT', '/api/v1/security/firewall', $admin, ['enabled' => true, 'allow_from' => 'not-an-ip'])->status());
        $res = $this->api('PUT', '/api/v1/security/firewall', $admin, ['enabled' => true, 'allow_from' => '203.0.113.7']);
        self::assertSame(202, $res->status(), $res->body());
        $sent = $this->lastCall('firewall.set_enabled');
        self::assertSame([true, true, '203.0.113.7'], [$sent['enabled'], $sent['allow_services'], $sent['allow_from']]);
        $this->api('PUT', '/api/v1/security/firewall', $admin, ['enabled' => false]);
        self::assertFalse($this->lastCall('firewall.set_enabled')['enabled']);

        $res = $this->api('PUT', '/api/v1/security/firewall/protection', $admin, ['enabled' => true, 'invalid' => true, 'syn_rate' => 100, 'conn_limits' => ['22:10', '443:150'], 'port_flood' => ['tcp/80:300'], 'exempt' => ['203.0.113.4']]);
        self::assertSame(202, $res->status(), $res->body());
        $sent = $this->lastCall('firewall.protection_set');
        self::assertSame([true, true, 100], [$sent['enabled'], $sent['invalid'], $sent['syn_rate']]);
        self::assertSame([['port' => 22, 'proto' => 'tcp', 'max' => 10], ['port' => 443, 'proto' => 'tcp', 'max' => 150]], $sent['conn_limits']);
        self::assertSame(['203.0.113.4'], $sent['exempt']);
        $calls = $this->calls('firewall.protection_set');
        self::assertSame(409, $this->api('PUT', '/api/v1/security/firewall/protection', $admin, ['syn_rate' => 'lots'])->status());
        self::assertSame(409, $this->api('PUT', '/api/v1/security/firewall/protection', $admin, ['conn_limits' => ['ssh:ten']])->status());
        self::assertSame($calls, $this->calls('firewall.protection_set'));
    }

    public function testCustomersAndReadOnlyTokensAreRefused(): void
    {
        $cust = $this->token($this->cust);
        $ro = $this->token($this->admin, 'read');
        foreach (['/api/v1/security/firewall', '/api/v1/security/pending', '/api/v1/security/firewall/protection'] as $p) {
            self::assertSame(403, $this->api('GET', $p, $cust)->status(), $p);
            self::assertSame(200, $this->api('GET', $p, $ro)->status(), $p);
        }
        $writes = $this->calls('firewall.rule_add') + $this->calls('firewall.set_enabled') + $this->calls('security.confirm');
        foreach ([['POST', '/security/firewall/rules', ['port' => '80']], ['PUT', '/security/firewall', ['enabled' => false]], ['PUT', '/security/firewall/protection', []], ['POST', '/security/confirm', ['id' => 'abc123abc123']], ['POST', '/security/undo', ['id' => 'abc123abc123']]] as [$m, $p, $b]) {
            self::assertSame(403, $this->api($m, '/api/v1' . $p, $cust, $b)->status(), "$m $p customer");
            self::assertSame(403, $this->api($m, '/api/v1' . $p, $ro, $b)->status(), "$m $p read-only");
        }
        self::assertSame($writes, $this->calls('firewall.rule_add') + $this->calls('firewall.set_enabled') + $this->calls('security.confirm'));
        self::assertSame(401, $this->api('GET', '/api/v1/security/firewall', null)->status());
    }
}
