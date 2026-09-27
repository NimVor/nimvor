<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\AutoSslService;
use WhaCP\Services\SettingsService;
use WhaCP\Services\WebsiteService;
use WhaCP\Support\DnsLookup;
use WhaCP\Tests\Support\HttpTestCase;

final class FakeDnsLookup extends DnsLookup
{
    /** @var array<string, list<string>> */
    public array $a = [];
    /** @var array<string, list<string>> */
    public array $ns = [];

    public function a(string $name): array
    {
        return $this->a[$name] ?? [];
    }

    public function ns(string $name): array
    {
        return $this->ns[$name] ?? [];
    }
}

/** Automatic SSL: issued once the domain points to this server. */
final class AutoSslTest extends HttpTestCase
{
    private Connection $db;
    private FakeDnsLookup $dnsLookup;
    private int $cust;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->dnsLookup = new FakeDnsLookup();
        $this->app->container->instance(DnsLookup::class, $this->dnsLookup);
        $this->app->container->get(SettingsService::class)->update(['dns_nameservers' => 'ns1.host.test, ns2.host.test', 'dns_default_ipv4' => '203.0.113.10'], null);
        $this->agent->succeed('ssl.issue', ['cert_file' => '/etc/letsencrypt/live/shop.test/fullchain.pem', 'key_file' => '/etc/letsencrypt/live/shop.test/privkey.pem']);
    }

    private function site(string $domain, string $aliases = ''): int
    {
        $users = $this->app->container->get(UserRepository::class);
        return $this->app->container->get(WebsiteService::class)->create(['domain' => $domain, 'kind' => 'static', 'aliases' => $aliases], (array) $users->find($this->cust));
    }

    /** @return array<string, mixed> */
    private function state(int $id): array
    {
        return (array) $this->db->fetch('SELECT https, auto_ssl, auto_ssl_tries, auto_ssl_next, auto_ssl_note FROM websites WHERE id = ?', [$id]);
    }

    private function pass(): void
    {
        $this->db->run("UPDATE websites SET auto_ssl_next = NULL WHERE auto_ssl = 'pending'");
        $this->app->container->get(AutoSslService::class)->run();
    }

    public function testIssuedOnceTheAddressPointsHere(): void
    {
        $id = $this->site('shop.test', 'www.shop.test');
        self::assertSame('pending', $this->state($id)['auto_ssl']);
        $this->pass();
        self::assertSame(0, $this->calls('ssl.issue'), 'not pointing here yet');
        self::assertSame('pending', $this->state($id)['auto_ssl']);
        self::assertGreaterThan(gmdate('c'), (string) $this->state($id)['auto_ssl_next'], 'checked again later');

        $this->dnsLookup->a['shop.test'] = ['203.0.113.10'];
        $this->pass();
        $call = $this->lastCall('ssl.issue');
        self::assertSame(['shop.test'], $call['names'], 'www does not point here: left out');
        self::assertSame('cust@example.test', $call['email']);
        $st = $this->state($id);
        self::assertSame([1, 'done'], [(int) $st['https'], $st['auto_ssl']]);
        $this->login('cust');
        self::assertStringContainsString('Certificate issued automatically', $this->request('GET', '/sites/' . $id . '/ssl')->body());
    }

    public function testIssuedWhenTheNameServersAreThisPanels(): void
    {
        $id = $this->site('shop.test', 'www.shop.test');
        $this->dnsLookup->ns['shop.test'] = ['ns1.host.test', 'ns9.elsewhere.test'];
        $this->pass();
        self::assertSame(0, $this->calls('ssl.issue'), 'only some name servers are ours');
        $this->dnsLookup->ns['shop.test'] = ['ns2.host.test', 'ns1.host.test'];
        $this->pass();
        self::assertSame(['shop.test', 'www.shop.test'], $this->lastCall('ssl.issue')['names'], 'www is under the same zone');
        self::assertSame('done', $this->state($id)['auto_ssl']);
    }

    public function testRetriesThenStops(): void
    {
        $id = $this->site('shop.test');
        $this->dnsLookup->a['shop.test'] = ['203.0.113.10'];
        $this->agent->fail('ssl.issue', 422, 'failed', 'Too many certificates already issued');
        $this->pass();
        $st = $this->state($id);
        self::assertSame(['pending', 1], [$st['auto_ssl'], (int) $st['auto_ssl_tries']]);
        self::assertStringContainsString('Too many certificates', (string) $st['auto_ssl_note']);
        for ($i = 0; $i < AutoSslService::MAX_TRIES; $i++) {
            $this->pass();
        }
        self::assertSame('failed', $this->state($id)['auto_ssl']);
        $calls = $this->calls('ssl.issue');
        $this->pass();
        self::assertSame($calls, $this->calls('ssl.issue'), 'no more tries');
        $this->login('cust');
        self::assertStringContainsString('Automatic SSL stopped', $this->request('GET', '/sites/' . $id . '/ssl')->body());
    }

    public function testGivesUpAfterThreeDaysAndCanBeTurnedOff(): void
    {
        $id = $this->site('shop.test');
        $this->db->run('UPDATE websites SET created_at = ? WHERE id = ?', [gmdate('c', time() - 4 * 86400), $id]);
        $this->pass();
        self::assertSame('gave_up', $this->state($id)['auto_ssl']);

        $this->app->container->get(SettingsService::class)->update(['auto_ssl' => '0'], null);
        self::assertSame('', $this->state($this->site('other.test'))['auto_ssl']);
    }
}
