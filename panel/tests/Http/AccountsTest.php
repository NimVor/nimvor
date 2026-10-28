<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Services\AccountOptions;
use NimVor\Services\SettingsService;
use NimVor\Tests\Support\HttpTestCase;

/** Create a New Account: login, plan, options and first website in one step. */
final class AccountsTest extends HttpTestCase
{
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run("INSERT INTO plans (name, created_at, updated_at) VALUES ('Starter', 'x', 'x')");
        $this->app->container->get(SettingsService::class)->update(['dns_default_ipv4' => '203.0.113.10'], null);
        $this->agent->succeed('sites.apply', ['changed' => []]);
    }

    /** @return array<string, string> */
    private function form(array $over = []): array
    {
        return $over + [
            'domain' => 'shop.test', 'username' => 'shopacct', 'email' => 'owner@shop.test',
            'password' => 'Correct-Horse-Battery-9', 'password_confirmation' => 'Correct-Horse-Battery-9',
            'plan_id' => (string) $this->db->scalar("SELECT id FROM plans WHERE name = 'Starter'"), 'php_version' => '8.3',
            'spam_filter' => '0', 'spam_box' => '1', 'mail_routing' => 'remote',
            'dns_dkim' => '1', 'dns_spf' => '1', 'dns_dmarc' => '1', 'dns_registrar_ns' => '0', 'dns_overwrite' => '0',
        ];
    }

    public function testAdministratorCreatesAnAccountWithItsWebsite(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/accounts/new')->body();
        foreach (['name="username"', 'data-nim-genpass', 'name="plan_id"', 'name="spam_box"', 'value="backup"', 'Make the account a reseller', 'v=spf1 +a +mx +ip4:203.0.113.10 ~all', 'v=DMARC1; p=none;'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $res = $this->post('/accounts', $this->form());
        $user = $this->db->fetch("SELECT u.*, r.name AS role FROM users u JOIN roles r ON r.id = u.role_id WHERE username = 'shopacct'");
        self::assertNotNull($user, (string) $this->flash('error'));
        self::assertSame('/users/' . $user['id'] . '/edit', $res->header('Location'));
        self::assertSame(['customer', null], [$user['role'], $user['parent_id']]);
        self::assertNotNull($user['plan_id']);
        $site = $this->db->fetch("SELECT * FROM websites WHERE domain = 'shop.test'");
        self::assertSame([(int) $user['id'], 'shopacct', 'php', '8.3'], [(int) $site['owner_id'], $site['system_user'], $site['kind'], $site['php_version']]);
        $spec = $this->lastCall('sites.apply');
        self::assertTrue($spec['new_user'], 'always a new system account');
        self::assertSame(['www.shop.test'], $spec['aliases']);
        $opts = $this->app->container->get(AccountOptions::class)->get((int) $user['id']);
        self::assertSame([false, true, 'remote', true], [$opts['spam_filter'], $opts['spam_box'], $opts['mail_routing'], $opts['dns_dmarc']]);

        // The options are on the account's page and can be changed.
        $edit = $this->request('GET', '/users/' . $user['id'] . '/edit')->body();
        self::assertStringContainsString('id="hosting-options"', $edit);
        $this->post('/users/' . $user['id'] . '/hosting-options', ['dns_dkim' => '0', 'dns_spf' => '1', 'dns_dmarc' => '1', 'spam_filter' => '1', 'spam_box' => '0', 'mail_routing' => 'local']);
        $opts = $this->app->container->get(AccountOptions::class)->get((int) $user['id']);
        self::assertSame([false, false, true, false, 'local'], [$opts['dns_dkim'], $opts['dns_dmarc'], $opts['spam_filter'], $opts['spam_box'], $opts['mail_routing']], 'DMARC needs DKIM');
    }

    public function testChecksComeFirstAndAFailedWebsiteRemovesTheLogin(): void
    {
        $this->login('admin');
        foreach ([['username' => 'shop.acct'], ['username' => 'root'], ['domain' => 'not a domain'], ['password_confirmation' => 'different-Password-1']] as $bad) {
            $this->post('/accounts', $this->form($bad));
        }
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM users WHERE email = 'owner@shop.test'"));
        self::assertSame(0, $this->calls('sites.apply'));

        // The system account exists on the server: the agent refuses; nothing is left behind.
        $this->agent->fail('sites.apply', 409, 'conflict', 'The system user shopacct exists already.');
        $this->post('/accounts', $this->form());
        self::assertStringContainsString('exists already', (string) $this->flash('error'));
        self::assertNull($this->db->scalar("SELECT id FROM users WHERE username = 'shopacct'"));
        self::assertNull($this->db->scalar("SELECT id FROM websites WHERE domain = 'shop.test'"));
    }

    public function testResellersCreateCustomersOnly(): void
    {
        $this->login('admin');
        $this->post('/accounts', $this->form(['username' => 'resell', 'email' => 'r@x.test', 'domain' => 'resell.test', 'reseller' => '1', 'plan_id' => '']));
        self::assertSame('reseller', $this->db->scalar("SELECT r.name FROM users u JOIN roles r ON r.id = u.role_id WHERE username = 'resell'"));
        $reseller = (int) $this->db->scalar("SELECT id FROM users WHERE username = 'resell'");
        $this->db->run("UPDATE users SET password_hash = (SELECT password_hash FROM users WHERE username = 'admin') WHERE id = ?", [$reseller]);

        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login('resell');
        self::assertStringNotContainsString('Make the account a reseller', $this->request('GET', '/accounts/new')->body());
        $this->post('/accounts', $this->form(['username' => 'sub', 'email' => 's@x.test', 'domain' => 'sub.test', 'reseller' => '1', 'plan_id' => '']));
        self::assertNull($this->db->scalar("SELECT id FROM users WHERE username = 'sub'"), 'resellers do not create resellers');
        $this->post('/accounts', $this->form(['username' => 'sub', 'email' => 's@x.test', 'domain' => 'sub.test', 'plan_id' => '']));
        $sub = $this->db->fetch("SELECT u.id, u.parent_id, r.name AS role FROM users u JOIN roles r ON r.id = u.role_id WHERE username = 'sub'");
        self::assertSame(['customer', $reseller], [$sub['role'], (int) $sub['parent_id']]);
        self::assertSame((int) $sub['id'], (int) $this->db->scalar("SELECT owner_id FROM websites WHERE domain = 'sub.test'"), 'the website is the customer\'s');
    }

    public function testSpamOptionsReachTheMailServer(): void
    {
        $this->login('admin');
        $this->post('/accounts', $this->form());
        $user = (int) $this->db->scalar("SELECT id FROM users WHERE username = 'shopacct'");
        $site = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'shop.test'");
        $this->db->run("INSERT INTO mail_domains (server_id, domain, website_id, owner_id, dkim_selector, dkim_txt, created_at) VALUES (1, 'shop.test', ?, ?, 's', 't', 'x')", [$site, $user]);
        $this->agent->succeed('mail.status', ['configured' => true, 'spam' => ['enabled' => true, 'add_header' => 7, 'reject' => 14]]);
        $this->agent->succeed('mail.spam_configure', ['ok' => true]);
        $this->post('/users/' . $user . '/hosting-options', ['spam_filter' => '0', 'spam_box' => '0', 'mail_routing' => 'auto', 'dns_dkim' => '1', 'dns_spf' => '1', 'dns_dmarc' => '1']);
        self::assertSame(['enabled' => true, 'add_header' => 7, 'reject' => 14, 'no_filter_domains' => ['shop.test'], 'no_junk_domains' => ['shop.test']], $this->lastCall('mail.spam_configure'));
    }
}
