<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Tests\Support\HttpTestCase;

/**
 * Changing the server host name passes the server's address to the agent
 * (so /etc/hosts resolves the name) and re-applies the mail server when
 * mail is set up, instead of leaving it to be edited by hand.
 */
final class HostnameChangeTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->agent->succeed('system.info', ['system' => ['interfaces' => [
            ['name' => 'eth0', 'addresses' => ['194.146.12.157/24', 'fe80::1/64']],
        ]]]);
        $this->agent->succeed('server.set_hostname', ['hostname' => 'mail.host.test', 'ip' => '194.146.12.157', 'hosts_updated' => true]);
    }

    public function testPropagatesAddressAndMail(): void
    {
        $this->agent->succeed('mail.status', ['configured' => true, 'hostname' => 'old.host.test']);
        $this->agent->succeed('mail.configure', ['hostname' => 'mail.host.test']);

        $this->login('admin');
        $this->post('/server/hostname', ['hostname' => 'mail.host.test']);

        $call = $this->lastCall('server.set_hostname');
        self::assertSame('mail.host.test', $call['hostname']);
        self::assertSame('194.146.12.157', $call['ip'], 'the server address is passed so the agent can fix /etc/hosts');

        self::assertSame(1, $this->calls('mail.configure'), 'mail is reconfigured with the new host name');
        self::assertSame('mail.host.test', $this->lastCall('mail.configure')['hostname']);
        self::assertStringContainsString('194.146.12.157', (string) $this->flash('success'));
    }

    public function testSkipsMailWhenNotConfigured(): void
    {
        $this->agent->succeed('mail.status', ['configured' => false]);

        $this->login('admin');
        $this->post('/server/hostname', ['hostname' => 'mail.host.test']);

        self::assertSame('mail.host.test', $this->lastCall('server.set_hostname')['hostname']);
        self::assertSame(0, $this->calls('mail.configure'), 'mail is left alone when it is not set up');
    }

    public function testCustomersCannot(): void
    {
        $this->login('cust');
        $this->post('/server/hostname', ['hostname' => 'evil.host.test']);
        self::assertSame(0, $this->calls('server.set_hostname'));
    }

    public function testHostnameReferencesAuditListsFindingsAndReappliesMail(): void
    {
        $this->login('admin');
        $this->agent->succeed('server.hostname_references', [
            'old' => 'vmi123.contaboserver.net', 'current' => 'vc100.oursvrs.com',
            'mail_hostname' => 'vmi123.contaboserver.net', 'mail_stale' => true, 'count' => 2,
            'findings' => [
                ['category' => 'dns', 'path' => '/etc/bind/whacp/db.shop.test', 'sample' => '@ IN MX 10 vmi123.contaboserver.net.'],
                ['category' => 'nginx', 'path' => '/etc/nginx/sites-available/whacp-hostname.conf', 'sample' => 'server_name vmi123.contaboserver.net;'],
            ],
        ]);

        $page = $this->request('GET', '/server/hostname-references', [], [], '', ['old' => 'vmi123.contaboserver.net'])->body();
        self::assertSame(['old' => 'vmi123.contaboserver.net'], $this->lastCall('server.hostname_references'));
        self::assertStringContainsString('DNS zones', $page);
        self::assertStringContainsString('db.shop.test', $page);
        self::assertStringContainsString('Re-apply mail server', $page);

        // Re-applying mail uses the current host name from system.info.
        $this->agent->succeed('system.info', ['system' => ['hostname' => 'vc100.oursvrs.com']]);
        $this->agent->succeed('mail.status', ['configured' => true]);
        $this->agent->succeed('mail.configure', ['hostname' => 'vc100.oursvrs.com']);
        $this->post('/server/hostname-references/mail');
        self::assertSame('vc100.oursvrs.com', $this->lastCall('mail.configure')['hostname']);
    }

    public function testHostnameReferencesIsAdminOnly(): void
    {
        $this->login('cust');
        self::assertNotSame(200, $this->request('GET', '/server/hostname-references', [], [], '', ['old' => 'x.test'])->status());
        $this->post('/server/hostname-references/mail');
        self::assertSame(0, $this->calls('mail.configure'));
    }
}
