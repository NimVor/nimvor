<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Services\SettingsService;
use NimVor\Tests\Support\HttpTestCase;

/** The Nameserver Selection page (PowerDNS, NSD, Knot DNS, BIND) and DNSSEC on a zone. */
final class DnsNameserverTest extends HttpTestCase
{
    private int $zoneId;
    private Connection $db;

    /** @return array<string, mixed> */
    private function backends(string $active = 'bind', int $signed = 0): array
    {
        $b = static fn (string $id, string $label, bool $installed, bool $running, array $dnssec, bool $replaces = false): array => [
            'id' => $id, 'label' => $label, 'active' => false, 'installed' => $installed, 'configured' => $installed, 'running' => $running,
            'advantages' => ['Fast.', 'Small.'], 'disadvantages' => ['Large footprint.'], 'notes' => [$label . ' note.'],
            'dnssec' => $dnssec, 'replaces_config' => $replaces, 'config_file' => '/etc/' . $id . '.conf',
        ];
        return ['backends' => [
            $b('powerdns', 'PowerDNS', false, false, ['supported' => true, 'reason' => '']),
            $b('nsd', 'NSD', false, false, ['supported' => false, 'reason' => 'NSD does not sign zones; DNSSEC needs the zones to be signed elsewhere.']),
            $b('knot', 'Knot DNS', false, false, ['supported' => true, 'reason' => ''], true),
            $b('bind', 'BIND', true, true, ['supported' => true, 'reason' => '']),
        ], 'active' => $active, 'zones' => 2, 'signed' => $signed];
    }

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->app->container->get(SettingsService::class)->update(['dns_nameservers' => 'ns1.host.test, ns2.host.test.', 'dns_default_ipv4' => '203.0.113.10'], null);
        $this->agent->succeed('dns.status', ['backend' => 'bind', 'backend_label' => 'BIND', 'installed' => true, 'configured' => true, 'running' => true, 'zones' => [], 'config_file' => '/etc/bind/named.conf.local']);
        $this->agent->succeed('dns.zone_apply', ['serial' => 2026090101, 'loaded' => true]);
        $this->agent->succeed('dns.backends', $this->backends());
        $this->login('admin');
        $this->post('/dns/zones', ['name' => 'plain.test']);
        $this->zoneId = (int) $this->db->scalar("SELECT id FROM dns_zones WHERE name = 'plain.test'");
    }

    public function testThePageShowsEveryNameserverLikeCpanelDoes(): void
    {
        $res = $this->request('GET', '/dns/nameserver');
        self::assertSame(200, $res->status());
        $page = $res->body();
        foreach (['PowerDNS', 'NSD', 'Knot DNS', 'BIND', 'Advantages', 'Disadvantages', 'Notes', 'name="backend" value="powerdns"', 'name="backend" value="nsd"', 'name="backend" value="knot"', 'name="backend" value="bind"', 'In use', 'Not installed', 'NSD does not sign zones', '/etc/knot.conf', 'name="confirm"'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertMatchesRegularExpression('~value="bind" id="ns-bind" checked~', $page, 'the nameserver in use is preselected');
        self::assertStringNotContainsString('confirm_dnssec', $page, 'no DNSSEC confirmation without signed zones');
        // The DNS page links to it and names the nameserver in use.
        $dns = $this->request('GET', '/dns')->body();
        self::assertStringContainsString('/dns/nameserver', $dns);
        self::assertStringContainsString('(BIND)', $dns);
    }

    public function testSignedZonesAskForAnExtraConfirmation(): void
    {
        $this->agent->succeed('dns.backends', $this->backends('bind', 3));
        $page = $this->request('GET', '/dns/nameserver')->body();
        self::assertStringContainsString('name="confirm_dnssec"', $page);
        self::assertStringContainsString('3 zones are signed', $page);
    }

    public function testChoosingStartsTheSwitchAsAJobAndIsAudited(): void
    {
        $this->agent->succeed('dns.backend_select', ['job_id' => 'job-ns-1', 'backend' => 'powerdns']);
        $res = $this->post('/dns/nameserver', ['backend' => 'powerdns', 'confirm' => '1']);
        self::assertSame('/dns/nameserver', $res->header('Location'));
        $call = $this->lastCall('dns.backend_select');
        self::assertSame(['powerdns', true, false], [$call['backend'], $call['confirm_edit_package_config'], $call['confirm_dnssec_reset']]);
        self::assertSame('job-ns-1', $this->flash('job'));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'dns.backend'"));
        $this->post('/dns/nameserver', ['backend' => 'knot', 'confirm' => '1', 'confirm_dnssec' => '1']);
        self::assertTrue($this->lastCall('dns.backend_select')['confirm_dnssec_reset']);
    }

    public function testAnUnknownNameserverIsRefusedWithoutAskingTheAgent(): void
    {
        $calls = $this->calls('dns.backend_select');
        $this->post('/dns/nameserver', ['backend' => 'dnsmasq', 'confirm' => '1']);
        self::assertStringContainsString('Choose PowerDNS', (string) json_encode($this->flash('errors')));
        self::assertSame($calls, $this->calls('dns.backend_select'));
    }

    public function testTheAgentsRefusalIsShown(): void
    {
        $this->agent->fail('dns.backend_select', 409, 'conflict', 'NimVor takes over /etc/knot/knot.conf. Confirm to continue.');
        $res = $this->post('/dns/nameserver', ['backend' => 'knot']);
        self::assertSame('/dns/nameserver', $res->header('Location'));
        self::assertStringContainsString('takes over', (string) json_encode($this->flash('error') ?? $this->flash('errors')));
    }

    public function testOnlyAdministratorsChooseTheNameserver(): void
    {
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login('cust');
        self::assertNotSame(200, $this->request('GET', '/dns/nameserver')->status());
        $calls = $this->calls('dns.backend_select');
        $this->post('/dns/nameserver', ['backend' => 'powerdns', 'confirm' => '1']);
        self::assertSame($calls, $this->calls('dns.backend_select'));
    }

    public function testTheZonePageOffersDnssecAndShowsTheDsRecords(): void
    {
        $this->agent->succeed('dns.dnssec_status', ['supported' => true, 'reason' => '', 'backend' => 'powerdns', 'backend_label' => 'PowerDNS', 'signed' => [], 'zone' => 'plain.test', 'enabled' => false, 'ds' => []]);
        $page = $this->request('GET', '/dns/zones/' . $this->zoneId)->body();
        self::assertStringContainsString('Turn on DNSSEC', $page);
        self::assertStringContainsString('/dns/zones/' . $this->zoneId . '/dnssec', $page);

        $this->agent->succeed('dns.dnssec_enable', ['zone' => 'plain.test', 'enabled' => true, 'changed' => true, 'ds' => []]);
        $res = $this->post('/dns/zones/' . $this->zoneId . '/dnssec');
        self::assertSame('/dns/zones/' . $this->zoneId, $res->header('Location'));
        self::assertSame('plain.test', $this->lastCall('dns.dnssec_enable')['zone']);
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'dns.dnssec_enable'"));

        $this->agent->succeed('dns.dnssec_status', ['supported' => true, 'reason' => '', 'backend' => 'powerdns', 'backend_label' => 'PowerDNS', 'signed' => ['plain.test'], 'zone' => 'plain.test', 'enabled' => true, 'ds' => [
            ['key_tag' => 35627, 'algorithm' => 13, 'digest_type' => 2, 'digest' => '2674be48661aa4afc421db66bd60731befe65cafcca6ceb92845bcda33ea9152'],
            ['key_tag' => 35627, 'algorithm' => 13, 'digest_type' => 4, 'digest' => 'aabbcc'],
        ]]);
        $page = $this->request('GET', '/dns/zones/' . $this->zoneId)->body();
        foreach (['Signed', '35627', 'ECDSA P-256/SHA-256 (13)', 'SHA-256 (2)', 'SHA-384 (4)', '2674be48661aa4afc421db66bd60731befe65cafcca6ceb92845bcda33ea9152', '35627 13 2 2674be48', 'Turn off DNSSEC', 'Remove its DS record at the registrar first'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $this->agent->succeed('dns.dnssec_disable', ['zone' => 'plain.test', 'enabled' => false, 'changed' => true]);
        $this->post('/dns/zones/' . $this->zoneId . '/dnssec/off');
        self::assertSame('plain.test', $this->lastCall('dns.dnssec_disable')['zone']);
    }

    public function testNsdSaysItCannotSign(): void
    {
        $this->agent->succeed('dns.dnssec_status', ['supported' => false, 'reason' => 'NSD does not sign zones; DNSSEC needs the zones to be signed elsewhere.', 'backend' => 'nsd', 'backend_label' => 'NSD', 'signed' => [], 'zone' => 'plain.test', 'enabled' => false, 'ds' => []]);
        $page = $this->request('GET', '/dns/zones/' . $this->zoneId)->body();
        self::assertStringContainsString('NSD does not sign zones', $page);
        self::assertStringNotContainsString('Turn on DNSSEC', $page);
    }

    public function testDnssecIsAnAdministratorsChoice(): void
    {
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login('cust');
        $calls = $this->calls('dns.dnssec_enable');
        $this->post('/dns/zones/' . $this->zoneId . '/dnssec');
        self::assertSame($calls, $this->calls('dns.dnssec_enable'));
    }
}
