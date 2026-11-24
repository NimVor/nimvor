<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\ApiTokenService;
use NimVor\Tests\Support\HttpTestCase;

/** REST API v1: ModSecurity. */
final class ModSecurityApiTest extends HttpTestCase
{
    private const RULE = 'SecRule REQUEST_URI "@contains /probe" "id:10001,phase:2,deny,status:403,msg:\'Block probes\'"';

    private Connection $db;
    private int $admin;
    private int $cust;
    private int $site;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run("INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, 'shop.test', 'static', '/home/u/shop.test', '/home/u/shop.test/public_html', 'u1', ?, 'active', 'x', 'x')", [$this->cust]);
        $this->site = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'shop.test'");
        $this->agent->succeed('modsec.status', [
            'module_loaded' => true, 'active' => true, 'connector' => ['state' => 'functional'], 'crs' => ['available' => true, 'version' => '4.0.0'],
            'config' => ['enabled' => true, 'detection_only' => false, 'crs' => ['enabled' => true, 'paranoia' => 1, 'inbound_threshold' => 5, 'outbound_threshold' => 4], 'custom' => ['enabled' => true]],
            'sites' => ['shop.test' => ['mode' => 'detect', 'exclude' => ['custom'], 'disabled_rules' => [942100]]],
            'vendors' => [['id' => 'crs', 'name' => 'OWASP CRS', 'description' => 'x']], 'custom_rules' => 1, 'audit_log_bytes' => 10, 'packages_available' => true,
        ]);
        $this->agent->succeed('modsec.custom_get', ['rules' => "# Block probes\n" . self::RULE . "\n"]);
        foreach (['modsec.configure', 'modsec.site'] as $a) {
            $this->agent->succeed($a, []);
        }
        $this->agent->succeed('modsec.custom_set', ['rules' => 2]);
        $this->agent->succeed('modsec.events', ['events' => [['time' => 't', 'host' => 'shop.test', 'client_ip' => '203.0.113.9', 'rules' => []]]]);
        $this->agent->succeed('modsec.installs', ['installs' => [['id' => '20261124T073000Z-ab12', 'method' => 'auto']]]);
        foreach (['modsec.update', 'modsec.install', 'modsec.crs_install', 'modsec.rollback'] as $a) {
            $this->agent->succeed($a, ['job_id' => 'job_1']);
        }
    }

    /** @param array<string, mixed> $json */
    private function api(string $method, string $path, ?string $token, array $json = [], array $query = []): Response
    {
        $h = ['content-type' => 'application/json'] + ($token === null ? [] : ['authorization' => 'Bearer ' . $token]);
        return $this->app->handle(new Request($method, $path, $query, [], ['REMOTE_ADDR' => '192.0.2.50'], $h, $json === [] ? '' : (string) json_encode($json)));
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

    public function testStatusAndConfigureAreForAdministrators(): void
    {
        $adm = $this->token($this->admin);
        $d = self::json($this->api('GET', '/api/v1/security/modsec', $adm))['data'];
        self::assertSame([true, true, 'functional', true, 1, 4], [$d['enabled'], $d['active'], $d['connector']['state'], $d['crs']['installed'], $d['crs']['paranoia'], $d['crs']['outbound_threshold']]);

        // Only what is sent changes.
        $res = $this->api('PATCH', '/api/v1/security/modsec', $adm, ['crs' => ['paranoia' => 3], 'detection_only' => true]);
        self::assertSame(200, $res->status(), $res->body());
        $sent = $this->lastCall('modsec.configure');
        self::assertSame([true, true, 3, 5, 4, true], [$sent['enabled'], $sent['crs']['enabled'], $sent['crs']['paranoia'], $sent['crs']['inbound_threshold'], $sent['crs']['outbound_threshold'], $sent['detection_only']]);
        self::assertNotNull($this->db->scalar("SELECT id FROM audit_logs WHERE action = 'modsec.configure'"));

        $calls = $this->calls('modsec.configure');
        self::assertSame(422, $this->api('PATCH', '/api/v1/security/modsec', $adm, ['crs' => ['paranoia' => 9]])->status());
        self::assertSame(422, $this->api('PATCH', '/api/v1/security/modsec', $adm, ['enabled' => 'yes'])->status());
        self::assertSame($calls, $this->calls('modsec.configure'));

        // A customer's token does not reach the server-wide settings.
        $cust = $this->token($this->cust);
        self::assertSame(403, $this->api('GET', '/api/v1/security/modsec', $cust)->status());
        self::assertSame(403, $this->api('PATCH', '/api/v1/security/modsec', $cust, ['enabled' => false])->status());
        self::assertSame(401, $this->api('GET', '/api/v1/security/modsec', null)->status());
        // A read-only token cannot change it.
        self::assertSame(403, $this->api('PATCH', '/api/v1/security/modsec', $this->token($this->admin, 'read'), ['enabled' => false])->status());
    }

    public function testCustomRules(): void
    {
        $adm = $this->token($this->admin);
        $d = self::json($this->api('GET', '/api/v1/security/modsec/rules', $adm))['data'];
        self::assertSame([1, 10001, 'deny', 'Block probes', 10002, true], [count($d['rules']), $d['rules'][0]['id'], $d['rules'][0]['action'], $d['rules'][0]['comment'], $d['next_id'], $d['enabled']]);
        self::assertSame(hash('sha256', self::RULE), $d['rules'][0]['sha256']);

        $new = 'SecRule ARGS "@contains evil" "id:10002,phase:2,deny,msg:\'Evil\'"';
        $res = $this->api('POST', '/api/v1/security/modsec/rules', $adm, ['rule' => $new, 'comment' => 'evil']);
        self::assertSame(201, $res->status(), $res->body());
        $sent = $this->lastCall('modsec.custom_set')['rules'];
        self::assertStringContainsString(self::RULE, $sent);
        self::assertStringContainsString("# evil\n" . $new, $sent);
        self::assertNotNull($this->db->scalar("SELECT id FROM audit_logs WHERE action = 'modsec.custom_rules'"));

        // Replace: the rule must still be the one that was read.
        $res = $this->api('PUT', '/api/v1/security/modsec/rules/0', $adm, ['rule' => $new, 'sha256' => hash('sha256', self::RULE)]);
        self::assertSame(200, $res->status(), $res->body());
        self::assertStringNotContainsString('/probe', $this->lastCall('modsec.custom_set')['rules']);
        $calls = $this->calls('modsec.custom_set');
        self::assertSame(409, $this->api('PUT', '/api/v1/security/modsec/rules/0', $adm, ['rule' => $new, 'sha256' => str_repeat('0', 64)])->status());
        self::assertSame(404, $this->api('PUT', '/api/v1/security/modsec/rules/5', $adm, ['rule' => $new, 'sha256' => hash('sha256', self::RULE)])->status());
        self::assertSame(422, $this->api('POST', '/api/v1/security/modsec/rules', $adm, ['rule' => '  '])->status());
        self::assertSame($calls, $this->calls('modsec.custom_set'));

        $res = $this->api('DELETE', '/api/v1/security/modsec/rules/0', $adm, [], ['sha256' => hash('sha256', self::RULE)]);
        self::assertSame([200, true], [$res->status(), self::json($res)['data']['deleted']]);
        self::assertSame('', $this->lastCall('modsec.custom_set')['rules']);
        self::assertSame(409, $this->api('DELETE', '/api/v1/security/modsec/rules/0', $adm, [], ['sha256' => 'wrong'])->status());
        self::assertSame(403, $this->api('POST', '/api/v1/security/modsec/rules', $this->token($this->cust), ['rule' => $new])->status());
    }

    public function testEventsInstallationAndJobs(): void
    {
        $adm = $this->token($this->admin);
        $d = self::json($this->api('GET', '/api/v1/security/modsec/events', $adm, [], ['limit' => '500']))['data'];
        self::assertSame([1, 200], [count($d['events']), $this->lastCall('modsec.events')['limit']]);
        self::assertSame('20261124T073000Z-ab12', self::json($this->api('GET', '/api/v1/security/modsec/installs', $adm))['data']['installs'][0]['id']);

        foreach (['update', 'install', 'crs'] as $op) {
            $res = $this->api('POST', '/api/v1/security/modsec/' . $op, $adm);
            self::assertSame([202, 'job_1'], [$res->status(), self::json($res)['data']['job_id']], $op);
        }
        self::assertSame(422, $this->api('POST', '/api/v1/security/modsec/install', $adm, ['method' => 'curl'])->status());
        self::assertSame(422, $this->api('POST', '/api/v1/security/modsec/install', $adm, ['nginx_source_sha256' => 'abc'])->status());
        self::assertSame('source', (function () use ($adm) {
            $this->api('POST', '/api/v1/security/modsec/install', $adm, ['method' => 'source', 'upgrade' => true]);
            return $this->lastCall('modsec.install')['method'];
        })());

        $res = $this->api('POST', '/api/v1/security/modsec/installs/20261124T073000Z-ab12/rollback', $adm);
        self::assertSame([202, '20261124T073000Z-ab12'], [$res->status(), $this->lastCall('modsec.rollback')['id']]);
        self::assertSame(404, $this->api('POST', '/api/v1/security/modsec/installs/20261124T073000Z-ffff/rollback', $adm)->status());
        self::assertNotNull($this->db->scalar("SELECT id FROM audit_logs WHERE action = 'modsec.rollback'"));
        self::assertSame(403, $this->api('POST', '/api/v1/security/modsec/update', $this->token($this->cust))->status());
    }

    public function testAWebsitesFirewall(): void
    {
        $cust = $this->token($this->cust);
        $base = '/api/v1/websites/' . $this->site . '/modsec';
        $d = self::json($this->api('GET', $base, $cust))['data'];
        self::assertSame(['shop.test', 'detect', ['custom'], [942100], true], [$d['domain'], $d['mode'], $d['vendors_off'], $d['disabled_rules'], $d['available']]);

        // The owner sets the mode; vendors and rules stay as they are.
        $res = $this->api('PUT', $base, $cust, ['mode' => 'on']);
        self::assertSame(200, $res->status(), $res->body());
        $sent = $this->lastCall('modsec.site');
        self::assertSame(['shop.test', 'on', ['custom'], [942100]], [$sent['domain'], $sent['mode'], $sent['exclude'], $sent['disabled_rules']]);
        self::assertNotNull($this->db->scalar("SELECT id FROM audit_logs WHERE action = 'modsec.site'"));

        $calls = $this->calls('modsec.site');
        self::assertSame(422, $this->api('PUT', $base, $cust, ['mode' => 'on', 'disabled_rules' => [1]])->status());
        self::assertSame(422, $this->api('PUT', $base, $cust, ['vendors_off' => ['crs']])->status());
        self::assertSame(409, $this->api('PUT', $base, $cust, ['mode' => 'sometimes'])->status());
        self::assertSame($calls, $this->calls('modsec.site'));

        // An administrator also sets vendors and single rules.
        $adm = $this->token($this->admin);
        $res = $this->api('PUT', $base, $adm, ['vendors_off' => ['crs'], 'disabled_rules' => [942100, 941100]]);
        self::assertSame(200, $res->status(), $res->body());
        $sent = $this->lastCall('modsec.site');
        self::assertSame(['detect', ['crs'], [942100, 941100]], [$sent['mode'], $sent['exclude'], $sent['disabled_rules']]);
        self::assertSame(422, $this->api('PUT', $base, $adm, ['vendors_off' => ['waf']])->status());
        self::assertSame(422, $this->api('PUT', $base, $adm, ['disabled_rules' => ['abc']])->status());

        $ev = self::json($this->api('GET', $base . '/events', $cust))['data']['events'];
        self::assertSame(1, count($ev));
        self::assertSame(['shop.test', 'www.shop.test'], $this->lastCall('modsec.events')['hosts']);
    }

    public function testAnotherAccountsWebsiteIsNotFound(): void
    {
        $other = $this->seedUser('other', 'customer');
        $t = $this->token($other);
        $base = '/api/v1/websites/' . $this->site . '/modsec';
        foreach ([['GET', $base], ['PUT', $base], ['GET', $base . '/events']] as [$m, $p]) {
            self::assertSame(404, $this->api($m, $p, $t, $m === 'PUT' ? ['mode' => 'off'] : [])->status(), "$m $p");
        }
    }
}
