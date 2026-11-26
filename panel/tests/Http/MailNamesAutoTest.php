<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\MailNamesAuto;
use NimVor\Services\SettingsService;
use NimVor\Services\WebsiteService;
use NimVor\Support\DnsLookup;
use NimVor\Tests\Support\HttpTestCase;

final class FakeMailNamesDns extends DnsLookup
{
    /** @var array<string, list<string>> */
    public array $a = [];

    public function a(string $name): array
    {
        return $this->a[$name] ?? [];
    }

    public function ns(string $name): array
    {
        return [];
    }
}

/** Mail names turn on by themselves once both names point to this server. */
final class MailNamesAutoTest extends HttpTestCase
{
    private Connection $db;
    private FakeMailNamesDns $lookup;
    private MailNamesAuto $auto;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->lookup = new FakeMailNamesDns();
        $this->app->container->instance(DnsLookup::class, $this->lookup);
        $this->app->container->get(SettingsService::class)->update(['dns_nameservers' => 'ns1.host.test'], null);
        $users = $this->app->container->get(UserRepository::class);
        $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'static'], (array) $users->find($cust));
        $this->agent->succeed('mail.status', ['installed' => ['postfix' => true, 'dovecot' => true, 'imap' => true, 'lmtp' => true, 'opendkim' => true], 'configured' => true, 'hostname' => 'mail.host.test', 'running' => [], 'domains' => [], 'mailboxes' => [], 'aliases' => []]);
        $this->agent->succeed('mail.domain_add', ['domain' => 'shop.test', 'dkim_selector' => 'nimvor202609', 'dkim_txt' => 'v=DKIM1; k=rsa; p=MIIBIjAN']);
        $this->agent->succeed('mail.names_status', ['domain' => 'shop.test', 'enabled' => false, 'mail_host' => 'mail.shop.test', 'webmail_host' => 'webmail.shop.test']);
        $this->agent->succeed('mail.names_enable', ['job_id' => 'aaaaaaaaaaaaaaaa']);
        $this->agent->succeed('net.addresses', ['addresses' => [['address' => '203.0.113.9', 'family' => 4, 'scope' => 'global']]]);
        $this->agent->succeed('dns.zone_apply', ['serial' => 2026090101, 'loaded' => true]);
        $this->auto = $this->app->container->get(MailNamesAuto::class);
        $this->login('cust');
        $this->post('/mail/domains', ['website_id' => (string) $this->siteId()]);
    }

    private function siteId(): int
    {
        return (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'shop.test'");
    }

    /** @return array<string, mixed> */
    private function row(): array
    {
        return (array) $this->db->fetch("SELECT * FROM mail_domains WHERE domain = 'shop.test'");
    }

    private function pass(): void
    {
        $this->db->run('UPDATE mail_domains SET names_auto_next = NULL');
        $this->auto->run();
    }

    public function testWaitsWhileTheNamesPointElsewhere(): void
    {
        $this->pass();
        self::assertSame(0, $this->calls('mail.names_enable'));
        self::assertSame('pending', $this->row()['names_auto']);
        self::assertStringContainsString('Waiting for mail.shop.test', (string) $this->row()['names_auto_note']);
    }

    public function testTurnsOnOnceBothNamesPointHere(): void
    {
        $this->lookup->a['mail.shop.test'] = ['203.0.113.9'];
        $this->pass();
        self::assertSame(0, $this->calls('mail.names_enable'), 'webmail does not point here yet');
        $this->lookup->a['webmail.shop.test'] = ['203.0.113.9'];
        $this->pass();
        self::assertSame('shop.test', $this->lastCall('mail.names_enable')['domain']);
        self::assertSame('enabling', $this->row()['names_auto']);
        $this->agent->succeed('mail.names_status', ['domain' => 'shop.test', 'enabled' => true, 'cert_ready' => true]);
        $this->pass();
        self::assertSame('done', $this->row()['names_auto']);
    }

    public function testAZoneHostedHereGetsItsRecordsAndTurnsOnAtOnce(): void
    {
        $this->post('/dns/zones', ['website_id' => (string) $this->siteId(), 'template' => '1']);
        $this->lookup->a['mail.shop.test'] = ['203.0.113.9'];
        $this->lookup->a['webmail.shop.test'] = ['203.0.113.9'];
        $this->pass();
        $records = array_map(static fn (array $r): string => $r['name'] . ' ' . $r['type'] . ' ' . $r['value'], $this->lastCall('dns.zone_apply')['records']);
        self::assertContains('mail A 203.0.113.9', $records);
        self::assertContains('webmail A 203.0.113.9', $records);
        self::assertSame('shop.test', $this->lastCall('mail.names_enable')['domain']);
    }

    public function testNeverOverrulesAnAdministratorWhoTurnedThemOff(): void
    {
        $this->lookup->a['mail.shop.test'] = ['203.0.113.9'];
        $this->lookup->a['webmail.shop.test'] = ['203.0.113.9'];
        $this->agent->succeed('mail.names_disable', ['enabled' => false]);
        $this->switchToAdmin();
        $this->post('/mail/domains/' . $this->row()['id'] . '/names/off');
        $this->pass();
        self::assertSame(0, $this->calls('mail.names_enable'));
        self::assertSame('off', $this->row()['names_auto']);
    }

    public function testGivesUpAfterTheWindow(): void
    {
        $this->db->run('UPDATE mail_domains SET names_auto_since = ?', [gmdate('c', time() - 8 * 86400)]);
        $this->pass();
        self::assertSame('gave_up', $this->row()['names_auto']);
        self::assertSame(0, $this->calls('mail.names_enable'));
    }

    public function testCanBeSwitchedOff(): void
    {
        $this->app->container->get(SettingsService::class)->update(['mail_names_auto' => '0'], null);
        $this->lookup->a['mail.shop.test'] = ['203.0.113.9'];
        $this->lookup->a['webmail.shop.test'] = ['203.0.113.9'];
        $this->pass();
        self::assertSame(0, $this->calls('mail.names_enable'));
    }

    private function switchToAdmin(): void
    {
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->login('admin');
    }
}
