<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\SettingsService;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

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
        foreach (['@ NS ns1.host.test.', '@ NS ns2.host.test.', '@ A 203.0.113.10', 'www CNAME @', '@ TXT v=spf1 a mx ~all', '@ CAA issue letsencrypt.org'] as $want) {
            self::assertContains($want, $summary);
        }
        self::assertSame(2026090101, (int) $this->db->scalar('SELECT serial FROM dns_zones WHERE id = ?', [$zoneId]));

        // Add a record: the whole zone is sent again.
        $this->post('/dns/zones/' . $zoneId . '/records', ['name' => 'mail.shop.test', 'type' => 'mx', 'value' => 'mx.provider.test.', 'priority' => '5']);
        $recs = $this->lastCall('dns.zone_apply')['records'];
        self::assertContains(['name' => 'mail', 'type' => 'MX', 'ttl' => 0, 'value' => 'mx.provider.test.', 'priority' => 5, 'weight' => 0, 'port' => 0], $recs);
        self::assertCount(7, $recs);

        // The agent refuses a change → the panel keeps the previous records.
        $this->agent->fail('dns.zone_apply', 422, 'invalid_params', 'record 8 (www A): www has a CNAME and other records');
        $this->post('/dns/zones/' . $zoneId . '/records', ['name' => 'www', 'type' => 'A', 'value' => '203.0.113.11']);
        self::assertStringContainsString('CNAME', (string) $this->flash('error'));
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
        $this->session = new \WhaCP\Http\Session\ArraySession();
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
