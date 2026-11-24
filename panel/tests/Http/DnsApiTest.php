<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\UserRepository;
use NimVor\Services\ApiTokenService;
use NimVor\Services\SettingsService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** REST API v1: DNS zones and records. */
final class DnsApiTest extends HttpTestCase
{
    private Connection $db;
    private int $admin;
    private int $cust;
    private int $custSite;
    private int $adminSite;

    protected function setUp(): void
    {
        parent::setUp();
        $this->admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'reseller');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->custSite = $sites->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $users->find($this->cust));
        $this->adminSite = $sites->create(['domain' => 'other.test', 'kind' => 'static'], (array) $users->find($this->admin));
        $this->db = $this->app->container->get(Connection::class);
        $this->app->container->get(SettingsService::class)->update(['dns_nameservers' => 'ns1.host.test, ns2.host.test.', 'dns_default_ipv4' => '203.0.113.10'], null);
        $this->agent->succeed('dns.status', ['installed' => true, 'configured' => true, 'running' => true, 'zones' => []]);
        $this->agent->succeed('dns.zone_apply', ['serial' => 2026090101, 'loaded' => true]);
        $this->agent->succeed('dns.zone_delete', ['removed' => true]);
        $this->agent->succeed('dns.zone_render', ['text' => "\$TTL 3600\n@ IN SOA ns1.host.test. hostmaster.shop.test. (…)\n"]);
        $this->agent->succeed('dns.zone_parse', ['records' => [
            ['name' => 'shop', 'type' => 'A', 'ttl' => 3600, 'value' => '192.0.2.10'],
            ['name' => 'api', 'type' => 'A', 'ttl' => 3600, 'value' => '192.0.2.11'],
        ], 'skipped' => [['line' => 3, 'text' => 'weird', 'reason' => 'not supported']]]);
        $this->agent->succeed('dns.dnssec_status', ['supported' => true, 'backend' => 'powerdns', 'backend_label' => 'PowerDNS', 'enabled' => false, 'ds' => []]);
        $this->agent->succeed('dns.dnssec_enable', ['warning' => '']);
        $this->agent->succeed('dns.dnssec_disable', ['ok' => true]);
    }

    /** @param array<string, mixed> $json @param array<string, string> $query */
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
        return $this->app->container->get(ApiTokenService::class)->create($userId, ['name' => 't' . bin2hex(random_bytes(2)), 'ability' => $ability, 'all_scopes' => '1', 'expires_days' => 30]);
    }

    public function testCustomerCreatesAndManagesTheZoneOfOwnWebsite(): void
    {
        $cust = $this->token($this->cust);
        $res = $this->api('POST', '/api/v1/dns/zones', $cust, ['website_id' => $this->custSite, 'template' => true]);
        self::assertSame(201, $res->status(), $res->body());
        $z = self::json($res)['data'];
        self::assertSame(['shop.test', $this->custSite, $this->cust, 'ns1.host.test'], [$z['name'], $z['website_id'], $z['owner_id'], $z['primary_ns']]);
        self::assertSame(2026090101, $z['serial']);
        self::assertGreaterThanOrEqual(6, $z['records']);
        self::assertSame([], $z['warnings']);
        $zid = $z['id'];

        // Records: list, add, replace, delete. Each change reloads the zone.
        $recs = self::json($this->api('GET', "/api/v1/dns/zones/$zid/records", $cust))['data'];
        self::assertContains('www', array_column($recs, 'name'));
        $calls = $this->calls('dns.zone_apply');
        $res = $this->api('POST', "/api/v1/dns/zones/$zid/records", $cust, ['name' => 'mail.shop.test', 'type' => 'mx', 'value' => 'mx.provider.test.', 'priority' => 5]);
        self::assertSame(201, $res->status(), $res->body());
        $rec = self::json($res)['data'];
        self::assertSame(['mail', 'MX', 5, false], [$rec['name'], $rec['type'], $rec['priority'], $rec['disabled']], 'name made relative, type upper-cased');
        self::assertSame($calls + 1, $this->calls('dns.zone_apply'));

        $res = $this->api('PUT', "/api/v1/dns/zones/$zid/records/{$rec['id']}", $cust, ['name' => 'mail', 'type' => 'MX', 'value' => 'mx2.provider.test.', 'priority' => 10, 'ttl' => 300]);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame(['mx2.provider.test.', 10, 300], [self::json($res)['data']['value'], self::json($res)['data']['priority'], self::json($res)['data']['ttl']]);

        $res = $this->api('DELETE', "/api/v1/dns/zones/$zid/records/{$rec['id']}", $cust);
        self::assertSame([200, $rec['id']], [$res->status(), self::json($res)['data']['deleted']]);
        self::assertSame(404, $this->api('DELETE', "/api/v1/dns/zones/$zid/records/{$rec['id']}", $cust)->status());
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'dns.record_delete'"));
    }

    public function testValidationAndConflictsAreReportedPerField(): void
    {
        $cust = $this->token($this->cust);
        $zid = self::json($this->api('POST', '/api/v1/dns/zones', $cust, ['website_id' => $this->custSite, 'template' => true]))['data']['id'];
        $calls = $this->calls('dns.zone_apply');
        foreach ([
            [['type' => 'A', 'name' => 'x', 'value' => 'not-an-ip'], 'value'],
            [['type' => 'AAAA', 'name' => 'x', 'value' => '192.0.2.1'], 'value'],
            [['type' => 'NOPE', 'name' => 'x', 'value' => 'y'], 'type'],
            [['type' => 'A', 'name' => 'bad name!', 'value' => '192.0.2.1'], 'name'],
            [['type' => 'A', 'name' => 'x', 'value' => '192.0.2.1', 'ttl' => 5], 'ttl'],
            [['type' => 'MX', 'name' => 'x', 'value' => '192.0.2.1', 'priority' => 5], 'value'],
            [['type' => 'A', 'name' => 'www', 'value' => '192.0.2.1'], 'name'], // www is a CNAME
        ] as [$body, $field]) {
            $res = $this->api('POST', "/api/v1/dns/zones/$zid/records", $cust, $body);
            self::assertSame(422, $res->status(), json_encode($body));
            self::assertArrayHasKey($field, self::json($res)['error']['fields'], json_encode($body));
        }
        self::assertSame($calls, $this->calls('dns.zone_apply'), 'nothing is sent to the DNS server for an invalid record');
        // A duplicate zone, a bad name, another account's website.
        self::assertSame(422, $this->api('POST', '/api/v1/dns/zones', $cust, ['website_id' => $this->custSite])->status(), 'the zone exists');
        self::assertArrayHasKey('website_id', self::json($this->api('POST', '/api/v1/dns/zones', $cust, ['website_id' => $this->adminSite]))['error']['fields']);
        self::assertArrayHasKey('name', self::json($this->api('POST', '/api/v1/dns/zones', $this->token($this->admin), ['name' => 'not a domain']))['error']['fields']);
    }

    public function testZonesAreScopedToTheirOwner(): void
    {
        $admin = $this->token($this->admin);
        $cust = $this->token($this->cust);
        $a = self::json($this->api('POST', '/api/v1/dns/zones', $admin, ['website_id' => $this->adminSite]))['data']['id'];
        $c = self::json($this->api('POST', '/api/v1/dns/zones', $cust, ['website_id' => $this->custSite]))['data']['id'];
        self::assertEqualsCanonicalizing(['other.test', 'shop.test'], array_column(self::json($this->api('GET', '/api/v1/dns/zones', $admin))['data'], 'name'));
        self::assertSame(['shop.test'], array_column(self::json($this->api('GET', '/api/v1/dns/zones', $cust))['data'], 'name'));
        foreach ([['GET', "/api/v1/dns/zones/$a"], ['GET', "/api/v1/dns/zones/$a/records"], ['POST', "/api/v1/dns/zones/$a/records"], ['DELETE', "/api/v1/dns/zones/$a"], ['POST', "/api/v1/dns/zones/$a/sync"], ['GET', "/api/v1/dns/zones/$a/export"]] as [$m, $p]) {
            self::assertSame(404, $this->api($m, $p, $cust, ['type' => 'A', 'value' => '192.0.2.1'])->status(), $m . ' ' . $p);
        }
        // A record of another zone is not reachable through your own zone's id.
        $other = (int) $this->db->scalar('SELECT MIN(id) FROM dns_records WHERE zone_id = ?', [$a]);
        self::assertSame(404, $this->api('DELETE', "/api/v1/dns/zones/$c/records/$other", $cust)->status());
        self::assertNotNull($this->db->scalar('SELECT id FROM dns_records WHERE id = ?', [$other]));
        // Read-only tokens change nothing.
        $read = $this->token($this->admin, 'read');
        self::assertSame(200, $this->api('GET', '/api/v1/dns/zones', $read)->status());
        self::assertSame(403, $this->api('POST', "/api/v1/dns/zones/$a/records", $read, ['type' => 'A', 'value' => '192.0.2.1'])->status());
    }

    public function testSettingsSyncExportImportAndBulk(): void
    {
        $admin = $this->token($this->admin);
        $zid = self::json($this->api('POST', '/api/v1/dns/zones', $admin, ['website_id' => $this->custSite, 'template' => true]))['data']['id'];
        $res = $this->api('PATCH', "/api/v1/dns/zones/$zid", $admin, ['ttl' => 1800, 'secondaries' => ['203.0.113.53']]);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame([1800, ['203.0.113.53']], [self::json($res)['data']['ttl'], self::json($res)['data']['secondaries']]);
        $res = $this->api('PATCH', "/api/v1/dns/zones/$zid", $admin, ['ttl' => 10]);
        self::assertSame(422, $res->status());
        self::assertSame(422, $this->api('PATCH', "/api/v1/dns/zones/$zid", $admin, ['secondaries' => ['nope']])->status());
        self::assertSame(['203.0.113.53'], self::json($this->api('PATCH', "/api/v1/dns/zones/$zid", $admin, ['ttl' => 900]))['data']['secondaries'], 'only what is sent changes');

        $before = $this->calls('dns.zone_apply');
        self::assertSame(200, $this->api('POST', "/api/v1/dns/zones/$zid/sync", $admin)->status());
        self::assertSame($before + 1, $this->calls('dns.zone_apply'));
        self::assertStringContainsString('SOA', self::json($this->api('GET', "/api/v1/dns/zones/$zid/export", $admin))['data']['zone_file']);

        $res = $this->api('POST', "/api/v1/dns/zones/$zid/import", $admin, ['zone_file' => "shop A 192.0.2.10\napi A 192.0.2.11\n"]);
        self::assertSame(200, $res->status(), $res->body());
        $d = self::json($res)['data'];
        self::assertSame([2, 1], [$d['added'], count($d['skipped'])]);
        self::assertSame(422, $this->api('POST', "/api/v1/dns/zones/$zid/import", $admin, ['zone_file' => '   '])->status());

        $ids = array_column(self::json($this->api('GET', "/api/v1/dns/zones/$zid/records", $admin))['data'], 'id');
        $pick = array_slice($ids, 0, 2);
        $res = $this->api('POST', "/api/v1/dns/zones/$zid/records/bulk", $admin, ['op' => 'disable', 'ids' => $pick]);
        self::assertSame([2, 'disable'], [self::json($res)['data']['changed'], self::json($res)['data']['op']]);
        self::assertSame(2, (int) $this->db->scalar('SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND disabled = 1', [$zid]));
        $res = $this->api('POST', "/api/v1/dns/zones/$zid/records/bulk", $admin, ['op' => 'explode', 'ids' => $pick]);
        self::assertSame(409, $res->status(), 'an unknown operation is refused');
        self::assertSame(409, $this->api('POST', "/api/v1/dns/zones/$zid/records/bulk", $admin, ['op' => 'delete', 'ids' => []])->status());
    }

    public function testDeleteNeedsTheZoneNameAndDnssecIsForAdministrators(): void
    {
        $admin = $this->token($this->admin);
        $cust = $this->token($this->cust);
        $zid = self::json($this->api('POST', '/api/v1/dns/zones', $cust, ['website_id' => $this->custSite]))['data']['id'];
        $res = $this->api('DELETE', "/api/v1/dns/zones/$zid", $cust, [], ['confirm' => 'wrong.test']);
        self::assertSame(422, $res->status());
        self::assertSame(0, $this->calls('dns.zone_delete'));
        self::assertNotNull($this->db->scalar('SELECT id FROM dns_zones WHERE id = ?', [$zid]));

        // DNSSEC changes how the zone is signed on the server: not for customers.
        foreach ([['GET', 'dnssec'], ['POST', 'dnssec/enable'], ['POST', 'dnssec/disable']] as [$m, $p]) {
            $res = $this->api($m, "/api/v1/dns/zones/$zid/$p", $cust);
            self::assertSame([403, 'forbidden'], [$res->status(), self::json($res)['error']['code']], $m . ' ' . $p);
        }
        self::assertSame(0, $this->calls('dns.dnssec_enable'));
        self::assertTrue(self::json($this->api('GET', "/api/v1/dns/zones/$zid/dnssec", $admin))['data']['supported']);
        self::assertSame(200, $this->api('POST', "/api/v1/dns/zones/$zid/dnssec/enable", $admin)->status());
        self::assertSame(1, $this->calls('dns.dnssec_enable'));
        self::assertSame(200, $this->api('POST', "/api/v1/dns/zones/$zid/dnssec/disable", $admin)->status());

        $res = $this->api('DELETE', "/api/v1/dns/zones/$zid", $cust, [], ['confirm' => 'shop.test']);
        self::assertSame(200, $res->status(), $res->body());
        self::assertSame('shop.test', self::json($res)['data']['name']);
        self::assertNull($this->db->scalar('SELECT id FROM dns_zones WHERE id = ?', [$zid]));
        self::assertSame(1, $this->calls('dns.zone_delete'));
    }

    public function testTheDnsServerRefusingAZoneChangesNothing(): void
    {
        $admin = $this->token($this->admin);
        $zid = self::json($this->api('POST', '/api/v1/dns/zones', $admin, ['website_id' => $this->custSite]))['data']['id'];
        $count = (int) $this->db->scalar('SELECT COUNT(*) FROM dns_records WHERE zone_id = ?', [$zid]);
        $this->agent->fail('dns.zone_apply', 500, 'failed', 'named-checkzone failed');
        $res = $this->api('POST', "/api/v1/dns/zones/$zid/records", $admin, ['name' => 'x', 'type' => 'A', 'value' => '192.0.2.1']);
        self::assertSame(502, $res->status());
        self::assertSame($count, (int) $this->db->scalar('SELECT COUNT(*) FROM dns_records WHERE zone_id = ?', [$zid]), 'the record was rolled back');
    }
}
