<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\SettingsService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

final class MailTest extends HttpTestCase
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
        $this->agent->succeed('mail.status', ['installed' => ['postfix' => true, 'dovecot' => true, 'imap' => true, 'lmtp' => true, 'opendkim' => true], 'configured' => true, 'hostname' => 'mail.host.test', 'running' => ['postfix' => true, 'dovecot' => true, 'opendkim' => true], 'domains' => [], 'mailboxes' => [['address' => 'info@shop.test', 'quota_mb' => 500, 'bytes' => 2048]], 'aliases' => []]);
        $this->agent->succeed('mail.domain_add', ['domain' => 'shop.test', 'dkim_selector' => 'nimvor202609', 'dkim_txt' => 'v=DKIM1; k=rsa; p=MIIBIjAN']);
        $this->agent->succeed('mail.mailbox_save', ['created' => true]);
        $this->agent->succeed('mail.mailbox_delete', ['deleted' => true]);
        $this->agent->succeed('mail.alias_save', ['ok' => true]);
        $this->agent->succeed('mail.alias_delete', ['deleted' => true]);
        $this->agent->succeed('mail.domain_remove', ['removed' => true]);
    }

    private function switchTo(string $user): void
    {
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login($user);
    }

    public function testCustomerHostsMailForOwnWebsite(): void
    {
        $this->login('cust');
        self::assertSame(200, $this->request('GET', '/mail')->status());
        $res = $this->post('/mail/domains', ['website_id' => (string) $this->siteId]);
        $id = (int) $this->db->scalar("SELECT id FROM mail_domains WHERE domain = 'shop.test'");
        self::assertSame('/mail/domains/' . $id, $res->header('Location'));
        $page = $this->request('GET', '/mail/domains/' . $id)->body();
        foreach (['nimvor202609._domainkey', 'v=DKIM1; k=rsa; p=MIIBIjAN', 'mail.host.test.', 'v=spf1 a mx ~all', 'v=DMARC1'] as $want) {
            self::assertStringContainsString($want, $page);
        }

        // Mailbox with a generated password shown once; the password never reaches the audit log.
        $this->post('/mail/domains/' . $id . '/mailboxes', ['local' => 'Info', 'generate' => '1', 'quota_mb' => '500']);
        $call = $this->lastCall('mail.mailbox_save');
        self::assertSame(['info@shop.test', 500], [$call['address'], $call['quota_mb']]);
        self::assertSame($call['password'], $this->flash('secret')['password']);
        self::assertStringNotContainsString($call['password'], (string) json_encode($this->db->fetchAll('SELECT * FROM audit_logs')));
        self::assertStringContainsString('2 KB', $this->request('GET', '/mail/domains/' . $id)->body() . ' ');
        foreach ([['local' => 'bad name', 'password' => 'a-long-password'], ['local' => 'x', 'password' => 'short'], ['local' => 'y'], ['local' => 'info', 'password' => 'a-long-password'], ['local' => 'weak', 'password' => 'abcdefghijkl']] as $bad) {
            $this->post('/mail/domains/' . $id . '/mailboxes', $bad);
            self::assertNotEmpty($this->flash('errors'), json_encode($bad));
        }
        self::assertSame(1, $this->calls('mail.mailbox_save'));

        // Aliases, including a catch-all; an alias cannot shadow a mailbox.
        $this->post('/mail/domains/' . $id . '/aliases', ['alias_local' => 'sales', 'destinations' => "info@shop.test, Boss@Gmail.com\n"]);
        self::assertSame(['sales@shop.test', ['info@shop.test', 'boss@gmail.com']], [$this->lastCall('mail.alias_save')['source'], $this->lastCall('mail.alias_save')['destinations']]);
        $this->post('/mail/domains/' . $id . '/aliases', ['alias_local' => '*', 'destinations' => 'info@shop.test']);
        self::assertSame('@shop.test', $this->lastCall('mail.alias_save')['source']);
        $this->post('/mail/domains/' . $id . '/aliases', ['alias_local' => 'info', 'destinations' => 'x@y.com']);
        self::assertArrayHasKey('alias_local', (array) $this->flash('errors'));
        $this->post('/mail/domains/' . $id . '/aliases', ['alias_local' => 'z', 'destinations' => 'not-an-address']);
        self::assertArrayHasKey('destinations', (array) $this->flash('errors'));

        // Another customer's website or domain is out of reach.
        $this->post('/mail/domains', ['website_id' => (string) $this->otherId]);
        $this->post('/mail/domains', ['domain' => 'gmail.com']);
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM mail_domains'));
        $this->switchTo('admin');
        $this->post('/mail/domains', ['domain' => 'admin-only.test']);
        $adminDomain = (int) $this->db->scalar("SELECT id FROM mail_domains WHERE domain = 'admin-only.test'");
        $this->switchTo('cust');
        self::assertSame(404, $this->request('GET', '/mail/domains/' . $adminDomain)->status());
        $calls = $this->calls('mail.mailbox_save');
        $this->post('/mail/domains/' . $adminDomain . '/mailboxes', ['local' => 'x', 'password' => 'a-long-password']);
        self::assertSame($calls, $this->calls('mail.mailbox_save'));
        self::assertSame('/mail/domains/' . $id, $this->request('GET', '/sites/' . $this->siteId . '/mail')->header('Location'));

        // The domain can only be removed once it is empty.
        $this->post('/mail/domains/' . $id . '/delete', ['confirm' => 'shop.test']);
        self::assertSame(0, $this->calls('mail.domain_remove'));
        $mb = (int) $this->db->scalar('SELECT id FROM mail_mailboxes');
        $this->post('/mail/domains/' . $id . '/mailboxes/' . $mb . '/delete', ['confirm' => 'wrong@shop.test']);
        self::assertSame(0, $this->calls('mail.mailbox_delete'));
        $this->post('/mail/domains/' . $id . '/mailboxes/' . $mb . '/delete', ['confirm' => 'info@shop.test']);
        self::assertSame('info@shop.test', $this->lastCall('mail.mailbox_delete')['address']);
        foreach ($this->db->fetchAll('SELECT id FROM mail_aliases') as $a) {
            $this->post('/mail/domains/' . $id . '/aliases/' . $a['id'] . '/delete');
        }
        $this->post('/mail/domains/' . $id . '/delete', ['confirm' => 'shop.test']);
        self::assertSame('shop.test', $this->lastCall('mail.domain_remove')['domain']);
    }

    public function testPublishesRecordsToTheDnsZone(): void
    {
        $this->app->container->get(SettingsService::class)->update(['dns_nameservers' => 'ns1.host.test'], null);
        $this->agent->succeed('dns.zone_apply', ['serial' => 2026090101, 'loaded' => true]);
        $this->login('cust');
        $this->post('/dns/zones', ['website_id' => (string) $this->siteId, 'template' => '1']);
        $this->post('/mail/domains', ['website_id' => (string) $this->siteId]);
        $id = (int) $this->db->scalar('SELECT id FROM mail_domains');
        $this->post('/mail/domains/' . $id . '/dns');
        self::assertStringContainsString('added', (string) $this->flash('success'));
        $records = array_map(static fn (array $r): string => $r['name'] . ' ' . $r['type'] . ' ' . $r['value'], $this->lastCall('dns.zone_apply')['records']);
        self::assertContains('@ MX mail.host.test.', $records);
        self::assertContains('nimvor202609._domainkey TXT v=DKIM1; k=rsa; p=MIIBIjAN', $records);
        self::assertSame(1, count(array_filter($records, static fn (string $r): bool => str_contains($r, 'v=spf1'))), 'the template SPF record is kept, not duplicated');
        // Publishing again adds nothing.
        $calls = $this->calls('dns.zone_apply');
        $this->post('/mail/domains/' . $id . '/dns');
        self::assertSame($calls, $this->calls('dns.zone_apply'));
    }

    public function testAnAdministratorGivesADomainItsOwnMailNames(): void
    {
        $this->app->container->get(SettingsService::class)->update(['dns_nameservers' => 'ns1.host.test'], null);
        $this->agent->succeed('dns.zone_apply', ['serial' => 2026090101, 'loaded' => true]);
        $this->agent->succeed('net.addresses', ['addresses' => [['address' => '203.0.113.9', 'family' => 4, 'scope' => 'global']]]);
        $this->agent->succeed('mail.names_status', ['domain' => 'shop.test', 'enabled' => false, 'mail_host' => 'mail.shop.test', 'webmail_host' => 'webmail.shop.test', 'sni_supported' => true, 'webmail_enabled' => true]);
        $this->agent->succeed('mail.names_enable', ['job_id' => 'aaaaaaaaaaaaaaaa', 'mail_host' => 'mail.shop.test', 'webmail_host' => 'webmail.shop.test']);
        $this->agent->succeed('mail.names_disable', ['enabled' => false]);
        $this->login('admin');
        $this->post('/dns/zones', ['website_id' => (string) $this->siteId, 'template' => '1']);
        $this->post('/mail/domains', ['website_id' => (string) $this->siteId]);
        $id = (int) $this->db->scalar("SELECT id FROM mail_domains WHERE domain = 'shop.test'");
        $page = $this->request('GET', '/mail/domains/' . $id)->body();
        self::assertStringContainsString('Turn on mail names', $page);
        self::assertStringContainsString('mail.shop.test', $page);
        self::assertStringContainsString('IMAP <code>mail.host.test</code>', $page, 'mail apps still use the server name until the names work');

        $this->post('/mail/domains/' . $id . '/names');
        self::assertSame('shop.test', $this->lastCall('mail.names_enable')['domain']);
        self::assertSame('admin@example.test', $this->lastCall('mail.names_enable')['email'], 'the administrator gets the certificate notices');
        $records = array_map(static fn (array $r): string => $r['name'] . ' ' . $r['type'] . ' ' . $r['value'], $this->lastCall('dns.zone_apply')['records']);
        self::assertContains('mail A 203.0.113.9', $records);
        self::assertContains('webmail A 203.0.113.9', $records);
        self::assertStringContainsString('Getting the certificate', (string) $this->flash('success'));

        // With the certificate in place the page offers the domain's own names to mail apps and for webmail.
        $this->agent->succeed('mail.names_status', ['domain' => 'shop.test', 'enabled' => true, 'cert_ready' => true, 'cert_days' => 80, 'mail_host' => 'mail.shop.test', 'webmail_host' => 'webmail.shop.test', 'sni_supported' => true, 'webmail_enabled' => true]);
        $this->agent->succeed('mail.status', ['installed' => ['postfix' => true, 'dovecot' => true, 'imap' => true, 'lmtp' => true, 'opendkim' => true], 'configured' => true, 'hostname' => 'mail.host.test', 'running' => ['postfix' => true, 'dovecot' => true, 'opendkim' => true], 'domains' => [], 'mailboxes' => [], 'aliases' => [], 'webmail' => ['enabled' => true, 'hostname' => 'webmail.host.test']]);
        $page = $this->request('GET', '/mail/domains/' . $id)->body();
        self::assertStringContainsString('IMAP <code>mail.shop.test</code> port 993', $page);
        self::assertStringContainsString('SMTP <code>mail.shop.test</code> port 587', $page);
        self::assertStringContainsString('https://webmail.shop.test/', $page);
        $this->post('/mail/domains/' . $id . '/names/off');
        self::assertSame('shop.test', $this->lastCall('mail.names_disable')['domain']);

        // A customer cannot change server-wide mail configuration.
        $before = $this->calls('mail.names_enable');
        $this->switchTo('cust');
        $this->post('/mail/domains/' . $id . '/names');
        $this->post('/mail/domains/' . $id . '/names/off');
        self::assertSame($before, $this->calls('mail.names_enable'));
        self::assertSame(1, $this->calls('mail.names_disable'));
    }

    public function testSpamFilter(): void
    {
        $this->login('admin');
        self::assertStringContainsString('Spam filter', $this->request('GET', '/mail')->body());
        $this->agent->succeed('mail.spam_configure', ['spam' => ['enabled' => true]]);
        $this->post('/mail/spam', ['enabled' => '1', 'add_header' => '15', 'reject' => '10']);
        self::assertArrayHasKey('add_header', (array) $this->flash('errors'));
        $this->post('/mail/spam', ['enabled' => '1', 'add_header' => '5.5', 'reject' => '14']);
        self::assertEquals([true, 5.5, 14, false], [$this->lastCall('mail.spam_configure')['enabled'], $this->lastCall('mail.spam_configure')['add_header'], $this->lastCall('mail.spam_configure')['reject'], $this->lastCall('mail.spam_configure')['confirm_replace']]);
        self::assertSame('The spam filter is on.', $this->flash('success'));
        // Customers cannot change it.
        $this->switchTo('cust');
        $this->post('/mail/spam', ['enabled' => '0']);
        self::assertSame(1, $this->calls('mail.spam_configure'));
    }

    public function testWebmail(): void
    {
        $this->login('admin');
        $status = ['installed' => ['postfix' => true], 'configured' => true, 'hostname' => 'mail.host.test', 'running' => ['postfix' => true], 'domains' => [], 'mailboxes' => [], 'aliases' => [], 'webmail' => ['installed' => false]];
        $this->agent->succeed('mail.status', $status);
        self::assertStringContainsString('Install Roundcube', $this->request('GET', '/mail')->body());
        $this->agent->succeed('mail.webmail_install', ['job_id' => 'job-webmail']);
        $this->post('/mail/webmail/install');
        self::assertSame(1, $this->calls('mail.webmail_install'));
        self::assertStringContainsString('Installing Roundcube', (string) $this->flash('success'));

        $status['webmail'] = ['installed' => true, 'enabled' => false];
        $this->agent->succeed('mail.status', $status);
        $body = $this->request('GET', '/mail')->body();
        self::assertStringContainsString('value="mail.host.test"', $body, 'the mail host name is suggested');
        $this->post('/mail/webmail', ['enabled' => '1', 'hostname' => 'not a host']);
        self::assertArrayHasKey('webmail_hostname', (array) $this->flash('errors'));
        self::assertSame(0, $this->calls('mail.webmail_configure'));
        $this->agent->succeed('mail.webmail_configure', ['enabled' => true, 'url' => 'https://webmail.host.test/', 'notes' => ['Users sign in…', 'A self-signed certificate is used.']]);
        $this->post('/mail/webmail', ['enabled' => '1', 'hostname' => 'WebMail.Host.test.']);
        self::assertSame(['enabled' => true, 'hostname' => 'webmail.host.test'], $this->lastCall('mail.webmail_configure'));
        self::assertSame('Webmail is on at https://webmail.host.test/. A self-signed certificate is used.', $this->flash('success'));

        // Mailbox owners see the address on their domain page.
        $status['webmail'] = ['installed' => true, 'enabled' => true, 'running' => true, 'hostname' => 'webmail.host.test', 'php' => '8.3'];
        $this->agent->succeed('mail.status', $status);
        self::assertStringContainsString('https://webmail.host.test/', $this->request('GET', '/mail')->body());
        $this->switchTo('cust');
        $this->post('/mail/domains', ['website_id' => (string) $this->siteId]);
        $id = (int) $this->db->scalar('SELECT id FROM mail_domains');
        $page = $this->request('GET', '/mail/domains/' . $id)->body();
        self::assertStringContainsString('href="https://webmail.host.test/"', $page);
        self::assertStringContainsString('port 993', $page);
        // Customers cannot turn it off.
        $this->post('/mail/webmail', ['enabled' => '0']);
        self::assertSame(1, $this->calls('mail.webmail_configure'));
    }

    public function testSendingOnPort465IsAnAdministratorsSwitch(): void
    {
        $this->login('admin');
        $status = ['installed' => ['postfix' => true], 'configured' => true, 'hostname' => 'mail.host.test', 'running' => ['postfix' => true], 'domains' => [], 'mailboxes' => [], 'aliases' => [], 'webmail' => ['installed' => false], 'smtps' => false];
        $this->agent->succeed('mail.status', $status);
        $body = $this->request('GET', '/mail')->body();
        self::assertStringContainsString('Sending on port 465', $body);
        self::assertStringNotContainsString('mail.host.test:465', $body, 'the line shows 465 only when it is on');

        $this->agent->succeed('mail.smtps_set', ['enabled' => true, 'changed' => true, 'notes' => []]);
        $this->post('/mail/smtps', ['enabled' => '1']);
        self::assertSame(['enabled' => true], $this->lastCall('mail.smtps_set'));
        self::assertStringContainsString('port 465 is on', (string) $this->flash('success'));

        $status['smtps'] = true;
        $this->agent->succeed('mail.status', $status);
        self::assertStringContainsString('mail.host.test:465</code> (SSL/TLS)', $this->request('GET', '/mail')->body());

        $this->post('/mail/smtps', ['enabled' => '0']);
        self::assertSame(['enabled' => false], $this->lastCall('mail.smtps_set'));
        self::assertSame(2, $this->calls('mail.smtps_set'));

        // A customer cannot change it.
        $this->switchTo('cust');
        $this->post('/mail/smtps', ['enabled' => '1']);
        self::assertSame(2, $this->calls('mail.smtps_set'));
    }

    public function testSetupNeedsAHostName(): void
    {
        $this->login('admin');
        $this->agent->succeed('mail.configure', ['configured' => true, 'notes' => ['Firewall: opened ports 25, 587 and 993.']]);
        $this->post('/mail/setup', ['hostname' => 'not a host', 'confirm' => '1']);
        self::assertArrayHasKey('hostname', (array) $this->flash('errors'));
        $this->post('/mail/setup', ['hostname' => 'Mail.Host.test.', 'confirm' => '1']);
        self::assertSame(['mail.host.test', true], [$this->lastCall('mail.configure')['hostname'], $this->lastCall('mail.configure')['confirm_edit_package_config']]);
        self::assertStringContainsString('ports 25', (string) $this->flash('success'));
    }
}
