<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\SettingsService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

final class DnsTest extends HttpTestCase
{
    private int $siteId;
    private int $otherId;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->siteId = $sites->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $users->find($cust));
        $this->otherId = $sites->create(['domain' => 'other.test', 'kind' => 'static'], (array) $users->find($admin));
        $this->db = $this->app->container->get(Connection::class);
        $this->app->container->get(SettingsService::class)->update(['dns_nameservers' => 'ns1.host.test, ns2.host.test.', 'dns_default_ipv4' => '203.0.113.10'], null);
        $this->agent->succeed('dns.status', ['installed' => true, 'configured' => true, 'running' => true, 'zones' => [], 'config_file' => '/etc/bind/named.conf.local']);
        $this->agent->succeed('dns.zone_apply', ['serial' => 2026090101, 'loaded' => true]);
    }

    public function testCustomerCreatesAndEditsTheZoneOfOwnWebsite(): void
    {
        $this->login('cust');
        self::assertSame(200, $this->request('GET', '/dns')->status());
        $res = $this->post('/dns/zones', ['website_id' => (string) $this->siteId, 'template' => '1']);
        $zoneId = (int) $this->db->scalar("SELECT id FROM dns_zones WHERE name = 'shop.test'");
        self::assertSame('/dns/zones/' . $zoneId, $res->header('Location'));
        $call = $this->lastCall('dns.zone_apply');
        self::assertSame('shop.test', $call['zone']);
        self::assertSame(['primary_ns' => 'ns1.host.test.', 'hostmaster' => 'hostmaster@shop.test'], $call['soa']);
        $summary = array_map(static fn (array $r): string => $r['name'] . ' ' . $r['type'] . ' ' . $r['value'], $call['records']);
        foreach (['@ NS ns1.host.test.', '@ NS ns2.host.test.', '@ A 203.0.113.10', 'www CNAME @', '@ TXT v=spf1 +a +mx +ip4:203.0.113.10 ~all', '@ CAA issue letsencrypt.org'] as $want) {
            self::assertContains($want, $summary);
        }
        self::assertSame(2026090101, (int) $this->db->scalar('SELECT serial FROM dns_zones WHERE id = ?', [$zoneId]));

        // Add a record: the whole zone is sent again.
        $this->post('/dns/zones/' . $zoneId . '/records', ['name' => 'mail.shop.test', 'type' => 'mx', 'value' => 'mx.provider.test.', 'priority' => '5']);
        $recs = $this->lastCall('dns.zone_apply')['records'];
        self::assertContains(['name' => 'mail', 'type' => 'MX', 'ttl' => 0, 'value' => 'mx.provider.test.', 'priority' => 5, 'weight' => 0, 'port' => 0], $recs);
        self::assertCount(7, $recs);

        // A CNAME conflict is caught before the zone is sent.
        $calls = $this->calls('dns.zone_apply');
        $this->post('/dns/zones/' . $zoneId . '/records', ['name' => 'www', 'type' => 'A', 'value' => '203.0.113.11']);
        self::assertStringContainsString('CNAME', (string) json_encode($this->flash('errors')));
        self::assertSame($calls, $this->calls('dns.zone_apply'));
        // The agent refuses a change → the panel keeps the previous records.
        $this->agent->fail('dns.zone_apply', 422, 'invalid_params', 'record 8 (ftp A): refused');
        $this->post('/dns/zones/' . $zoneId . '/records', ['name' => 'ftp', 'type' => 'A', 'value' => '203.0.113.11']);
        self::assertStringContainsString('refused', (string) $this->flash('error'));
        self::assertSame(7, (int) $this->db->scalar('SELECT COUNT(*) FROM dns_records WHERE zone_id = ?', [$zoneId]));
        $this->agent->succeed('dns.zone_apply', ['serial' => 2026090103, 'loaded' => true]);

        // Customers cannot host other domains or touch other zones.
        $this->post('/dns/zones', ['website_id' => (string) $this->otherId]);
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM dns_zones WHERE name = 'other.test'"));
        $this->post('/dns/zones', ['name' => 'google.com']);
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM dns_zones'));
        $this->switchTo('admin');
        $this->post('/dns/zones', ['name' => 'admin-only.test']);
        $adminZone = (int) $this->db->scalar("SELECT id FROM dns_zones WHERE name = 'admin-only.test'");
        self::assertGreaterThan(0, $adminZone);
        $this->switchTo('cust');
        self::assertSame(404, $this->request('GET', '/dns/zones/' . $adminZone)->status());
        $calls = $this->calls('dns.zone_apply');
        $this->post('/dns/zones/' . $adminZone . '/records', ['name' => 'x', 'type' => 'A', 'value' => '1.2.3.4']);
        self::assertSame($calls, $this->calls('dns.zone_apply'));

        // The website tab leads to the zone.
        self::assertSame('/dns/zones/' . $zoneId, $this->request('GET', '/sites/' . $this->siteId . '/dns')->header('Location'));

        // Delete needs the typed name.
        $this->agent->succeed('dns.zone_delete', ['deleted' => true]);
        $this->post('/dns/zones/' . $zoneId . '/delete', ['confirm' => 'wrong']);
        self::assertSame(0, $this->calls('dns.zone_delete'));
        $this->post('/dns/zones/' . $zoneId . '/delete', ['confirm' => 'shop.test']);
        self::assertSame('shop.test', $this->lastCall('dns.zone_delete')['zone']);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM dns_records WHERE zone_id = ?', [$zoneId]));
    }

    private function switchTo(string $user): void
    {
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login($user);
    }

    public function testRecordValidation(): void
    {
        $this->login('admin');
        $this->post('/dns/zones', ['name' => 'v.test']);
        $id = (int) $this->db->scalar("SELECT id FROM dns_zones WHERE name = 'v.test'");
        $calls = $this->calls('dns.zone_apply');
        foreach ([['name' => 'a b', 'type' => 'A', 'value' => '1.2.3.4'], ['name' => 'x', 'type' => 'PTR', 'value' => 'a.'], ['name' => 'x', 'type' => 'TXT', 'value' => "a\nb"], ['name' => 'x', 'type' => 'A', 'value' => '1.2.3.4', 'ttl' => '5']] as $bad) {
            $this->post('/dns/zones/' . $id . '/records', $bad);
            self::assertNotEmpty($this->flash('errors'), json_encode($bad));
        }
        self::assertSame($calls, $this->calls('dns.zone_apply'));
        $this->post('/dns/zones/' . $id . '/settings', ['ttl' => '600', 'secondaries' => '198.51.100.2, 2001:db8::2']);
        self::assertSame(['198.51.100.2', '2001:db8::2'], $this->lastCall('dns.zone_apply')['secondaries']);
        self::assertSame(600, $this->lastCall('dns.zone_apply')['ttl']);
    }

    public function testTypedFieldsForSrvAndCaaAndPtrOnlyInReverseZones(): void
    {
        $this->login('admin');
        $this->post('/dns/zones', ['name' => 'v.test', 'template' => '0']);
        $id = (int) $this->db->scalar("SELECT id FROM dns_zones WHERE name = 'v.test'");
        // SRV from service + protocol fields.
        $this->post('/dns/zones/' . $id . '/records', ['type' => 'SRV', 'name' => '@', 'srv_service' => 'sip', 'srv_protocol' => 'tcp', 'priority' => '10', 'weight' => '60', 'port' => '5060', 'value' => 'sip.v.test.']);
        self::assertContains(['name' => '_sip._tcp', 'type' => 'SRV', 'ttl' => 0, 'value' => 'sip.v.test.', 'priority' => 10, 'weight' => 60, 'port' => 5060], $this->lastCall('dns.zone_apply')['records']);
        // CAA from flags, tag and value.
        $this->post('/dns/zones/' . $id . '/records', ['type' => 'CAA', 'name' => '@', 'caa_flags' => '128', 'caa_tag' => 'iodef', 'caa_value' => 'mailto:sec@v.test']);
        self::assertContains(['name' => '@', 'type' => 'CAA', 'ttl' => 0, 'value' => 'iodef mailto:sec@v.test', 'priority' => 128, 'weight' => 0, 'port' => 0], $this->lastCall('dns.zone_apply')['records']);
        $calls = $this->calls('dns.zone_apply');
        foreach ([
            ['type' => 'CAA', 'name' => '@', 'caa_flags' => '1', 'caa_tag' => 'issue', 'caa_value' => 'x.test'],
            ['type' => 'CAA', 'name' => '@', 'caa_flags' => '0', 'caa_tag' => 'nope', 'caa_value' => 'x.test'],
            ['type' => 'SRV', 'name' => '@', 'srv_service' => 'bad service', 'srv_protocol' => 'tcp', 'port' => '1', 'value' => 'a.'],
            ['type' => 'SRV', 'name' => '_x._tcp', 'port' => '70000', 'value' => 'a.'],
            ['type' => 'PTR', 'name' => '10', 'value' => 'host.v.test.'],
        ] as $bad) {
            $this->post('/dns/zones/' . $id . '/records', $bad);
            self::assertNotEmpty($this->flash('errors'), (string) json_encode($bad));
        }
        self::assertSame($calls, $this->calls('dns.zone_apply'));
        // PTR in a reverse zone.
        $this->post('/dns/zones', ['name' => '2.0.192.in-addr.arpa', 'template' => '0']);
        $rev = (int) $this->db->scalar("SELECT id FROM dns_zones WHERE name = '2.0.192.in-addr.arpa'");
        self::assertGreaterThan(0, $rev);
        $this->post('/dns/zones/' . $rev . '/records', ['type' => 'PTR', 'name' => '10', 'value' => 'host.v.test.']);
        self::assertSame([], $this->flash('errors') ?? []);
        self::assertContains('10 PTR host.v.test.', array_map(static fn (array $r): string => $r['name'] . ' ' . $r['type'] . ' ' . $r['value'], $this->lastCall('dns.zone_apply')['records']));
    }

    public function testBulkImportExportAndWarnings(): void
    {
        $this->login('cust');
        $this->post('/dns/zones', ['website_id' => (string) $this->siteId, 'template' => '1']);
        $id = (int) $this->db->scalar("SELECT id FROM dns_zones WHERE name = 'shop.test'");
        $rid = fn (string $name, string $type): int => (int) $this->db->scalar('SELECT id FROM dns_records WHERE zone_id = ? AND name = ? AND type = ?', [$id, $name, $type]);

        // Import: new records are added, duplicates and @ NS kept, conflicts and unsupported lines listed.
        $this->agent->succeed('dns.zone_parse', ['records' => [
            ['name' => '@', 'type' => 'NS', 'ttl' => 0, 'value' => 'ns9.elsewhere.test.', 'priority' => 0, 'weight' => 0, 'port' => 0],
            ['name' => '@', 'type' => 'A', 'ttl' => 0, 'value' => '203.0.113.10', 'priority' => 0, 'weight' => 0, 'port' => 0],
            ['name' => 'www', 'type' => 'A', 'ttl' => 0, 'value' => '203.0.113.12', 'priority' => 0, 'weight' => 0, 'port' => 0],
            ['name' => 'blog', 'type' => 'A', 'ttl' => 300, 'value' => '203.0.113.12', 'priority' => 0, 'weight' => 0, 'port' => 0],
            ['name' => '@', 'type' => 'TXT', 'ttl' => 0, 'value' => 'v=spf1 -all', 'priority' => 0, 'weight' => 0, 'port' => 0],
        ], 'skipped' => [['line' => 9, 'text' => 'x HINFO a b', 'reason' => 'HINFO records are not supported']]]);
        $this->post('/dns/zones/' . $id . '/import', ['zonefile' => "blog 300 IN A 203.0.113.12\n"]);
        self::assertSame('shop.test', $this->lastCall('dns.zone_parse')['zone']);
        self::assertStringContainsString('2 record(s) imported', (string) $this->flash('success'));
        $skipped = (array) $this->session->pullFlash('dns_import');
        self::assertCount(2, $skipped);
        self::assertStringContainsString('CNAME', (string) $skipped[1]['reason']);
        self::assertGreaterThan(0, $rid('blog', 'A'));
        self::assertSame(0, $rid('www', 'A'));
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND value = 'ns9.elsewhere.test.'", [$id]));
        $this->post('/dns/zones/' . $id . '/import', ['zonefile' => '   ']);
        self::assertNotEmpty($this->flash('errors'));

        // Two SPF records now: the page warns.
        $page = $this->request('GET', '/dns/zones/' . $id)->body();
        self::assertStringContainsString('2 SPF records', $page);

        // Bulk: turned off records are left out of the served zone.
        $blog = $rid('blog', 'A');
        $spf2 = (int) $this->db->scalar("SELECT id FROM dns_records WHERE zone_id = ? AND value = 'v=spf1 -all'", [$id]);
        $this->post('/dns/zones/' . $id . '/bulk', ['op' => 'disable', 'ids' => [(string) $blog, (string) $spf2]]);
        $served = array_map(static fn (array $r): string => $r['name'] . ' ' . $r['value'], $this->lastCall('dns.zone_apply')['records']);
        self::assertNotContains('blog 203.0.113.12', $served);
        self::assertSame(2, (int) $this->db->scalar('SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND disabled = 1', [$id]));
        self::assertStringNotContainsString('2 SPF records', $this->request('GET', '/dns/zones/' . $id)->body());
        $this->post('/dns/zones/' . $id . '/bulk', ['op' => 'enable', 'ids' => [(string) $blog]]);
        self::assertContains('blog 203.0.113.12', array_map(static fn (array $r): string => $r['name'] . ' ' . $r['value'], $this->lastCall('dns.zone_apply')['records']));
        $this->post('/dns/zones/' . $id . '/bulk', ['op' => 'delete', 'ids' => [(string) $blog, (string) $spf2]]);
        self::assertSame(0, $rid('blog', 'A'));
        // Unknown operations and other zones' records are refused.
        $calls = $this->calls('dns.zone_apply');
        $this->post('/dns/zones/' . $id . '/bulk', ['op' => 'drop', 'ids' => [(string) $rid('@', 'A')]]);
        self::assertSame($calls, $this->calls('dns.zone_apply'));
        $this->switchTo('admin');
        $this->post('/dns/zones', ['name' => 'admin-only.test', 'template' => '0']);
        $adminZone = (int) $this->db->scalar("SELECT id FROM dns_zones WHERE name = 'admin-only.test'");
        $calls = $this->calls('dns.zone_apply');
        $this->switchTo('cust');
        $this->post('/dns/zones/' . $adminZone . '/bulk', ['op' => 'delete', 'ids' => [(string) $rid('@', 'A')]]);
        $this->post('/dns/zones/' . $id . '/bulk', ['op' => 'delete', 'ids' => [(string) $this->db->scalar('SELECT id FROM dns_records WHERE zone_id = ? LIMIT 1', [$adminZone])]]);
        self::assertSame($calls, $this->calls('dns.zone_apply'));
        self::assertGreaterThan(0, (int) $this->db->scalar('SELECT COUNT(*) FROM dns_records WHERE zone_id = ?', [$adminZone]));

        // Export: the rendered zone file as a download.
        $this->agent->succeed('dns.zone_render', ['text' => "\$ORIGIN shop.test.\n@ IN A 203.0.113.10\n"]);
        $res = $this->request('GET', '/dns/zones/' . $id . '/export');
        self::assertSame(200, $res->status());
        self::assertStringContainsString('attachment', (string) $res->header('Content-Disposition'));
        self::assertStringContainsString('203.0.113.10', $res->body());
        self::assertSame('shop.test', $this->lastCall('dns.zone_render')['zone']);
        self::assertSame('/dns', $this->request('GET', '/dns/zones/' . $adminZone . '/export')->header('Location'));
    }

    public function testNeedsNameServers(): void
    {
        $this->app->container->get(SettingsService::class)->update(['dns_nameservers' => ''], null);
        $this->login('admin');
        $this->post('/dns/zones', ['name' => 'n.test']);
        self::assertStringContainsString('name servers', (string) $this->flash('error'));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM dns_zones'));
    }

    public function testSetupNeedsConfirmation(): void
    {
        $this->login('admin');
        $this->agent->succeed('dns.configure', ['configured' => true, 'notes' => ['Firewall: opened port 53 (TCP and UDP).']]);
        $this->post('/dns/setup', ['confirm' => '1']);
        self::assertTrue($this->lastCall('dns.configure')['confirm_edit_package_config']);
        self::assertStringContainsString('port 53', (string) $this->flash('success'));
    }
}
