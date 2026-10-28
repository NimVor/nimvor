<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\AccountOptions;
use NimVor\Services\SettingsService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/**
 * DNS (and email with DKIM) for new websites, when this server hosts DNS;
 * never inside another account's domain.
 */
final class SiteProvisionerTest extends HttpTestCase
{
    private Connection $db;
    private int $cust;
    private int $other;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->other = $this->seedUser('mallory', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->app->container->get(SettingsService::class)->update(['dns_nameservers' => 'ns1.host.test, ns2.host.test', 'dns_default_ipv4' => '203.0.113.10'], null);
        $this->agent->succeed('dns.status', ['installed' => true, 'configured' => true, 'running' => true, 'zones' => []]);
        $this->agent->succeed('dns.zone_apply', ['serial' => 2026092701, 'loaded' => true]);
        $this->agent->succeed('dns.zone_delete', ['deleted' => true]);
    }

    private function site(string $domain, int $owner): int
    {
        $users = $this->app->container->get(UserRepository::class);
        return $this->app->container->get(WebsiteService::class)->create(['domain' => $domain, 'kind' => 'static'], (array) $users->find($owner));
    }

    /** @return list<string> "name TYPE value" of the zone */
    private function records(string $zone): array
    {
        return array_map(static fn (array $r): string => $r['name'] . ' ' . $r['type'] . ' ' . $r['value'], $this->db->fetchAll('SELECT r.* FROM dns_records r JOIN dns_zones z ON z.id = r.zone_id WHERE z.name = ? ORDER BY r.id', [$zone]));
    }

    public function testANewWebsiteGetsItsZoneWithMailRecords(): void
    {
        $this->agent->succeed('mail.status', ['installed' => ['postfix' => true], 'configured' => true, 'hostname' => 'mail.host.test', 'running' => ['postfix' => true], 'mailboxes' => []]);
        $this->agent->succeed('mail.domain_add', ['domain' => 'shop.test', 'dkim_selector' => 'nimvor202609', 'dkim_txt' => 'v=DKIM1; k=rsa; p=MIIB']);
        $id = $this->site('shop.test', $this->cust);
        $zone = $this->db->fetch("SELECT * FROM dns_zones WHERE name = 'shop.test'");
        self::assertSame([$id, $this->cust], [(int) $zone['website_id'], (int) $zone['owner_id']]);
        $r = $this->records('shop.test');
        foreach (['@ NS ns1.host.test.', '@ A 203.0.113.10', 'www CNAME @', '@ TXT v=spf1 +a +mx +ip4:203.0.113.10 ~all', '_dmarc TXT v=DMARC1; p=none;', '@ CAA issue letsencrypt.org', '@ MX mail.host.test.', 'nimvor202609._domainkey TXT v=DKIM1; k=rsa; p=MIIB'] as $want) {
            self::assertContains($want, $r, $want);
        }
        self::assertSame(1, count(array_filter($r, static fn ($x) => str_contains($x, 'v=spf1'))), 'one SPF record');
        self::assertSame(1, count(array_filter($r, static fn ($x) => str_contains($x, 'v=DMARC1'))), 'one DMARC record');
        self::assertSame($id, (int) $this->db->scalar("SELECT website_id FROM mail_domains WHERE domain = 'shop.test'"));

        // A website under that domain, same account: an A record in its zone.
        $this->site('blog.shop.test', $this->cust);
        self::assertContains('blog A 203.0.113.10', $this->records('shop.test'));
        self::assertNull($this->db->scalar("SELECT id FROM dns_zones WHERE name = 'blog.shop.test'"));
    }

    public function testNeverInsideAnotherAccountsDomain(): void
    {
        $this->site('shop.test', $this->cust);
        $before = $this->records('shop.test');
        // Another customer cannot even create the website...
        $this->login('mallory');
        $this->post('/websites/new', ['domain' => 'pay.shop.test', 'kind' => 'static']);
        self::assertNull($this->db->scalar("SELECT id FROM websites WHERE domain = 'pay.shop.test'"));
        // ...nor a zone for it, and when an administrator creates the
        // website for them, no record and no zone are added.
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $id = $sites->create(['domain' => 'pay.shop.test', 'kind' => 'static', 'owner_id' => (string) $this->other], ['role_scope' => 'all', 'id' => 1] + (array) $users->find($this->other));
        $this->db->run('UPDATE websites SET owner_id = ? WHERE id = ?', [$this->other, $id]);
        $this->post('/dns/zones', ['website_id' => (string) $id, 'template' => '1']);
        self::assertNull($this->db->scalar("SELECT id FROM dns_zones WHERE name = 'pay.shop.test'"));
        self::assertSame($before, $this->records('shop.test'));
    }

    public function testAccountOptions(): void
    {
        $opts = $this->app->container->get(AccountOptions::class);
        // The registrar's name servers: no zone here.
        $opts->set($this->cust, ['dns_registrar_ns' => true]);
        $this->site('reg.test', $this->cust);
        self::assertNull($this->db->scalar("SELECT id FROM dns_zones WHERE name = 'reg.test'"));

        // Without SPF there is no DMARC either.
        $opts->set($this->cust, ['dns_registrar_ns' => false, 'dns_spf' => false]);
        $this->site('nospf.test', $this->cust);
        $r = $this->records('nospf.test');
        self::assertContains('@ A 203.0.113.10', $r);
        self::assertSame([], array_values(array_filter($r, static fn ($x) => str_contains($x, 'v=spf1') || str_contains($x, 'DMARC'))));

        // An existing zone is kept, unless the account asks to overwrite it.
        $this->db->run("INSERT INTO dns_zones (server_id, name, website_id, owner_id, primary_ns, hostmaster, created_at, updated_at) VALUES (1, 'old.test', NULL, ?, 'ns1.host.test', 'h@x.test', 'x', 'x')", [$this->cust]);
        $old = (int) $this->db->lastInsertId();
        $this->db->run("INSERT INTO dns_records (zone_id, name, type, ttl, value, priority, weight, port) VALUES (?, '@', 'A', 0, '198.51.100.1', 0, 0, 0)", [$old]);
        $this->site('old.test', $this->cust);
        self::assertSame(['@ A 198.51.100.1'], $this->records('old.test'));
        $this->db->run("DELETE FROM websites WHERE domain = 'old.test'");
        $this->db->run("DELETE FROM domains WHERE name = 'old.test'");
        $opts->set($this->cust, ['dns_overwrite' => true]);
        $this->site('old.test', $this->cust);
        self::assertNotContains('@ A 198.51.100.1', $this->records('old.test'));
        self::assertContains('@ A 203.0.113.10', $this->records('old.test'));
    }

    public function testNothingWithoutADnsServer(): void
    {
        $this->agent->fail('dns.status', 503, 'unavailable', 'down');
        $id = $this->site('quiet.test', $this->cust);
        self::assertGreaterThan(0, $id, 'the website is created');
        self::assertNull($this->db->scalar("SELECT id FROM dns_zones WHERE name = 'quiet.test'"));
    }
}
