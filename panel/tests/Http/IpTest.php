<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\SettingsService;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

final class IpTest extends HttpTestCase
{
    private Connection $db;
    private int $siteId;
    private int $otherId;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->siteId = $sites->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $users->find($cust));
        $this->otherId = $sites->create(['domain' => 'blog.test', 'kind' => 'static'], (array) $users->find($admin));
        $this->db = $this->app->container->get(Connection::class);
        $this->app->container->get(SettingsService::class)->update(['dns_nameservers' => 'ns1.host.test, ns2.host.test', 'dns_default_ipv4' => '203.0.113.10'], null);
        $this->agent->succeed('net.addresses', ['addresses' => [
            ['interface' => 'lo', 'address' => '127.0.0.1', 'family' => 4, 'prefix_len' => 8, 'scope' => 'host'],
            ['interface' => 'eth0', 'address' => '203.0.113.10', 'family' => 4, 'prefix_len' => 24, 'scope' => 'global'],
            ['interface' => 'eth0', 'address' => '203.0.113.11', 'family' => 4, 'prefix_len' => 24, 'scope' => 'global'],
            ['interface' => 'eth0', 'address' => 'fe80::1', 'family' => 6, 'prefix_len' => 64, 'scope' => 'link'],
        ], 'gateways' => ['203.0.113.1'], 'ipv6_ranges' => [], 'ipv6' => true]);
        $this->agent->succeed('sites.apply', ['domain' => 'x', 'changed' => [], 'user' => ['name' => 'x', 'uid' => 1500, 'gid' => 1500]]);
        $this->agent->succeed('dns.status', ['installed' => true, 'configured' => true, 'running' => true, 'zones' => []]);
        $this->agent->succeed('dns.zone_apply', ['serial' => 2026090101, 'loaded' => true]);
        $this->agent->succeed('net.ipv6_ranges', ['ipv6_ranges' => []]);
    }

    private function poolId(string $ip): int
    {
        return (int) $this->db->scalar('SELECT id FROM ip_addresses WHERE address = ?', [$ip]);
    }

    public function testPoolAddRebuildReserveDelete(): void
    {
        $this->login('admin');
        $this->post('/ips', ['address' => '198.51.100.9', 'kind' => 'shared']);
        self::assertStringContainsString('not configured on this server', (string) json_encode($this->flash('errors')));
        foreach (['127.0.0.1', 'fe80::1', 'nonsense'] as $bad) {
            $this->post('/ips', ['address' => $bad]);
            self::assertNotEmpty($this->flash('errors'), $bad);
        }
        $this->post('/ips', ['address' => '203.0.113.11', 'kind' => 'dedicated']);
        self::assertGreaterThan(0, $this->poolId('203.0.113.11'));
        $this->post('/ips/rebuild', []);
        self::assertGreaterThan(0, $this->poolId('203.0.113.10'));
        self::assertSame(2, (int) $this->db->scalar('SELECT COUNT(*) FROM ip_addresses'));
        $page = $this->request('GET', '/ips')->body();
        self::assertStringContainsString('On the server', $page);

        // Reserving needs a reason; a reserved address cannot be assigned.
        $this->post('/ips/' . $this->poolId('203.0.113.11'), ['kind' => 'dedicated', 'reserved' => '1', 'reason' => '']);
        self::assertNotEmpty($this->flash('errors'));
        $this->post('/ips/' . $this->poolId('203.0.113.11'), ['kind' => 'dedicated', 'reserved' => '1', 'reason' => 'mail relay']);
        $this->post('/sites/' . $this->siteId . '/ip', ['ipv4' => '203.0.113.11']);
        self::assertStringContainsString('reserved', (string) json_encode($this->flash('errors')));
        $this->post('/ips/' . $this->poolId('203.0.113.11') . '/delete', []);
        self::assertSame(0, $this->poolId('203.0.113.11'));
    }

    public function testAssignSiteFollowsDnsAndRollsBack(): void
    {
        $this->login('admin');
        $this->post('/dns/zones', ['website_id' => (string) $this->siteId, 'template' => '1']);
        $zone = (int) $this->db->scalar("SELECT id FROM dns_zones WHERE name = 'shop.test'");
        $this->post('/ips/rebuild', []);
        $this->post('/ips/' . $this->poolId('203.0.113.11'), ['kind' => 'dedicated']);

        $this->post('/sites/' . $this->siteId . '/ip', ['ipv4' => '203.0.113.11', 'dns' => '1']);
        self::assertStringContainsString('DNS record', (string) $this->flash('success'));
        self::assertSame('203.0.113.11', $this->lastCall('sites.apply')['ipv4']);
        self::assertSame('203.0.113.11', $this->db->scalar('SELECT ipv4 FROM websites WHERE id = ?', [$this->siteId]));
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND type = 'A' AND value = '203.0.113.10'", [$zone]));
        self::assertGreaterThan(0, (int) $this->db->scalar("SELECT COUNT(*) FROM dns_records WHERE zone_id = ? AND type = 'A' AND value = '203.0.113.11'", [$zone]));
        // The SPF record stays as it was (only A/AAAA records follow).
        self::assertStringContainsString('ip4:203.0.113.10', (string) $this->db->scalar("SELECT value FROM dns_records WHERE zone_id = ? AND type = 'TXT'", [$zone]));

        // Dedicated: another website cannot take it.
        $this->post('/sites/' . $this->otherId . '/ip', ['ipv4' => '203.0.113.11']);
        self::assertStringContainsString('dedicated to shop.test', (string) json_encode($this->flash('errors')));

        // The DNS server refuses → the website goes back to its address.
        $this->agent->fail('dns.zone_apply', 422, 'invalid_params', 'refused');
        $this->post('/sites/' . $this->siteId . '/ip', ['ipv4' => '', 'dns' => '1']);
        self::assertStringContainsString('moved back', (string) $this->flash('error'));
        self::assertSame('203.0.113.11', $this->db->scalar('SELECT ipv4 FROM websites WHERE id = ?', [$this->siteId]));
        self::assertSame('203.0.113.11', $this->lastCall('sites.apply')['ipv4']);
    }

    public function testRangesAllocateAndMigrate(): void
    {
        $this->login('admin');
        $this->post('/ips/ranges', ['prefix' => '2001:DB8:5::/64', 'note' => 'routed']);
        self::assertSame(['2001:db8:5::/64'], $this->lastCall('net.ipv6_ranges')['ranges']);
        $rid = (int) $this->db->scalar('SELECT id FROM ipv6_ranges');
        $this->post('/ips/ranges/' . $rid . '/allocate', []);
        $this->post('/ips/ranges/' . $rid . '/allocate', ['website_id' => (string) $this->siteId]);
        self::assertSame(['2001:db8:5::10', '2001:db8:5::11'], array_column($this->db->fetchAll('SELECT address FROM ip_addresses ORDER BY id'), 'address'));
        self::assertSame('2001:db8:5::11', $this->db->scalar('SELECT ipv6 FROM websites WHERE id = ?', [$this->siteId]));
        self::assertSame('2001:db8:5::11', $this->lastCall('sites.apply')['ipv6']);
        $this->post('/ips/ranges/' . $rid . '/allocate', ['address' => '2001:db8:6::1']);
        self::assertNotEmpty($this->flash('errors'));
        $this->post('/ips/ranges/' . $rid . '/delete', []);
        self::assertStringContainsString('shop.test', (string) $this->flash('error'));

        // Migration: preview, then move.
        $page = $this->request('GET', '/ips/migrate', query: ['source' => '2001:db8:5::11', 'dest' => '2001:db8:5::10'])->body();
        self::assertStringContainsString('shop.test', $page);
        $this->post('/ips/migrate', ['source' => '2001:db8:5::11', 'dest' => '2001:db8:5::10']);
        self::assertNotEmpty($this->flash('error'), 'needs confirmation');
        $this->post('/ips/migrate', ['source' => '2001:db8:5::11', 'dest' => '2001:db8:5::10', 'confirm' => '1']);
        self::assertSame('2001:db8:5::10', $this->db->scalar('SELECT ipv6 FROM websites WHERE id = ?', [$this->siteId]));
        $this->post('/ips/rebuild', []);
        $mixed = $this->request('GET', '/ips/migrate', query: ['source' => '203.0.113.10', 'dest' => '2001:db8:5::11'])->body();
        self::assertStringContainsString('Both addresses must be IPv4, or both IPv6', $mixed);
    }

    public function testMigrationOfAllZonesAndDefault(): void
    {
        $this->login('admin');
        $this->post('/dns/zones', ['website_id' => (string) $this->otherId, 'template' => '1']);
        $this->post('/ips/rebuild', []);
        $this->post('/ips/migrate', ['source' => '203.0.113.10', 'dest' => '203.0.113.11', 'all_zones' => '1', 'confirm' => '1']);
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM dns_records WHERE type = 'A' AND value = '203.0.113.10'"));
        self::assertSame('203.0.113.11', $this->app->container->get(SettingsService::class)->string('dns_default_ipv4'));
    }

    public function testCustomersCannotManageIps(): void
    {
        $this->login('cust');
        foreach (['/ips', '/ips/ranges', '/ips/migrate'] as $u) {
            self::assertNotSame(200, $this->request('GET', $u)->status(), $u);
        }
        $this->post('/ips', ['address' => '203.0.113.11']);
        $this->post('/sites/' . $this->siteId . '/ip', ['ipv4' => '203.0.113.11']);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM ip_addresses'));
        self::assertNull($this->db->scalar('SELECT ipv4 FROM websites WHERE id = ?', [$this->siteId]));
        self::assertStringNotContainsString('id="ip"', $this->request('GET', '/sites/' . $this->siteId . '/advanced')->body());
    }
}
