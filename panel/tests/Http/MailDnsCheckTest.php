<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Tests\Support\HttpTestCase;

/** The email authentication checker on the mail domain page. */
final class MailDnsCheckTest extends HttpTestCase
{
    private int $domain;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $this->seedUser('other', 'customer');
        $db = $this->app->container->get(Connection::class);
        $db->run("INSERT INTO mail_domains (server_id, domain, owner_id, dkim_selector, dkim_txt, routing, created_at) VALUES (1, 'shop.test', ?, 'mail', 'v=DKIM1; p=KEY', 'remote', 'x')", [$cust]);
        $this->domain = (int) $db->lastInsertId();
        $this->app->container->get(\NimVor\Services\SettingsService::class)->update(['dns_default_ipv4' => '203.0.113.5'], null);
        $this->agent->succeed('mail.status', ['hostname' => 'mx.host.test', 'mailboxes' => []]);
        $this->agent->succeed('mail.auth_check', ['checks' => [
            ['id' => 'spf', 'label' => 'SPF', 'status' => 'fail', 'found' => 'v=spf1 -all', 'detail' => 'The record rejects mail <b>'],
        ]]);
    }

    public function testTheOwnerChecksTheDomain(): void
    {
        $this->login('cust');
        $page = $this->request('GET', '/mail/domains/' . $this->domain)->body();
        self::assertStringContainsString('data-nim-dns-check="/mail/domains/' . $this->domain . '/dns-check"', $page);
        $res = $this->request('GET', '/mail/domains/' . $this->domain . '/dns-check', [], ['accept' => 'application/json']);
        self::assertSame(200, $res->status());
        $json = json_decode($res->body(), true);
        self::assertSame('fail', $json['checks'][0]['status']);
        self::assertSame('The record rejects mail <b>', $json['checks'][0]['detail'], 'JSON; the page inserts it as text');
        $call = $this->lastCall('mail.auth_check');
        self::assertSame(['shop.test', 'mx.host.test', ['203.0.113.5'], 'mail', 'v=DKIM1; p=KEY', 'remote'], [$call['domain'], $call['hostname'], $call['ips'], $call['dkim_selector'], $call['dkim_txt'], $call['routing']]);
    }

    public function testOthersCannotCheckTheDomain(): void
    {
        $this->login('other');
        self::assertSame(404, $this->request('GET', '/mail/domains/' . $this->domain . '/dns-check', [], ['accept' => 'application/json'])->status());
        self::assertSame(0, $this->calls('mail.auth_check'));
    }
}
