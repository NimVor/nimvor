<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

final class FtpTest extends HttpTestCase
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
        $this->siteId = $sites->create(['domain' => 'Shop.test', 'kind' => 'static', 'system_user' => 'shopuser'], (array) $users->find($cust));
        $this->otherId = $sites->create(['domain' => 'other.test', 'kind' => 'static', 'system_user' => 'otheruser'], (array) $users->find($admin));
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('ftp.status', ['installed' => true, 'configured' => true, 'active' => true, 'settings' => ['tls' => 'required', 'passive_min' => 40110, 'passive_max' => 40210], 'certificate' => ['subject' => 'CN=host', 'sha256' => 'AB12', 'not_after' => '2036-01-01T00:00:00Z'], 'accounts' => [], 'sftp' => [], 'sftp_root' => '/home/nimvor', 'other_ftp_servers' => []]);
        $this->agent->succeed('ftp.account_save', ['created' => true]);
        $this->agent->succeed('ftp.account_delete', ['deleted' => 'x']);
        $this->agent->succeed('sftp.configure', ['enabled' => true]);
    }

    public function testCustomerManagesAccountsOfOwnWebsite(): void
    {
        $this->login('cust');
        $page = $this->request('GET', '/sites/' . $this->siteId . '/ftp');
        self::assertSame(200, $page->status());
        self::assertStringContainsString('@shop.test', $page->body());
        self::assertSame('shopuser', $this->lastCall('ftp.status')['user'], 'only this website\'s accounts are asked for');

        $this->post('/sites/' . $this->siteId . '/ftp/accounts', ['name' => 'Designer', 'dir' => 'public_html/', 'password' => 'a-long-password', 'quota_mb' => '200']);
        $call = $this->lastCall('ftp.account_save');
        self::assertSame(['designer@shop.test', 'shopuser', 'public_html', 200, 'a-long-password'], [$call['name'], $call['user'], $call['dir'], $call['quota_mb'], $call['password']]);
        $audit = (string) $this->db->scalar("SELECT details FROM audit_logs WHERE action = 'ftp.account_create'");
        self::assertStringContainsString('designer@shop.test', $audit);
        self::assertStringNotContainsString('a-long-password', $audit);

        // Generated passwords are shown once.
        $this->post('/sites/' . $this->siteId . '/ftp/accounts', ['name' => 'dev', 'generate' => '1']);
        $pw = $this->lastCall('ftp.account_save')['password'];
        self::assertGreaterThanOrEqual(20, strlen($pw));
        self::assertSame($pw, $this->flash('secret')['password']);

        // Other websites' accounts cannot be touched through this website.
        $calls = $this->calls('ftp.account_delete');
        $this->post('/sites/' . $this->siteId . '/ftp/accounts/delete', ['name' => 'admin@other.test']);
        self::assertSame($calls, $this->calls('ftp.account_delete'));
        $this->post('/sites/' . $this->siteId . '/ftp/accounts', ['existing' => '1', 'name' => 'admin@other.test', 'password' => 'a-long-password']);
        self::assertSame('dev@shop.test', $this->lastCall('ftp.account_save')['name']);
        $this->post('/sites/' . $this->siteId . '/ftp/accounts/delete', ['name' => 'designer@shop.test']);
        self::assertSame(['designer@shop.test', 'shopuser'], [$this->lastCall('ftp.account_delete')['name'], $this->lastCall('ftp.account_delete')['user']]);
        // And the other website is not reachable at all.
        self::assertSame(404, $this->request('GET', '/sites/' . $this->otherId . '/ftp')->status());
        $saves = $this->calls('ftp.account_save');
        $this->post('/sites/' . $this->otherId . '/ftp/accounts', ['name' => 'x', 'password' => 'a-long-password']);
        self::assertSame($saves, $this->calls('ftp.account_save'));
        // The FTP server itself is for administrators.
        self::assertNotSame(200, $this->request('GET', '/ftp')->status());
    }

    public function testValidation(): void
    {
        $this->login('cust');
        foreach ([['name' => 'bad name', 'password' => 'a-long-password'], ['name' => 'ok', 'password' => 'short'], ['name' => 'ok'], ['name' => 'ok', 'password' => 'a-long-password', 'dir' => '../../etc']] as $bad) {
            $this->post('/sites/' . $this->siteId . '/ftp/accounts', $bad);
            self::assertNotEmpty($this->flash('errors'), json_encode($bad));
        }
        self::assertSame(0, $this->calls('ftp.account_save'));
    }

    public function testSftp(): void
    {
        $this->login('cust');
        $key = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl me@laptop';
        $this->post('/sites/' . $this->siteId . '/sftp', ['enabled' => '1', 'keys' => "# my laptop\n" . $key . "\n\n", 'password_login' => '1', 'sftp_password' => 'sftp-long-password']);
        $call = $this->lastCall('sftp.configure');
        self::assertSame(['shopuser', true, [$key], true, 'sftp-long-password'], [$call['user'], $call['enabled'], $call['keys'], $call['password_login'], $call['password']]);
        self::assertStringNotContainsString('sftp-long-password', (string) $this->db->scalar("SELECT details FROM audit_logs WHERE action = 'sftp.configure'"));
        $this->post('/sites/' . $this->siteId . '/sftp', []);
        self::assertFalse($this->lastCall('sftp.configure')['enabled']);
    }

    public function testAdministratorSetsUpTheServer(): void
    {
        $this->login('admin');
        self::assertStringContainsString('nimvor-ftpd.service', $this->request('GET', '/ftp')->body());
        $this->agent->succeed('ftp.configure', ['notes' => ['Firewall: opened 21/tcp and 40110:40210/tcp.'], 'stopped' => []]);
        $this->post('/ftp/configure', ['passive_min' => '40000', 'passive_max' => '40100', 'max_clients' => '50', 'max_per_ip' => '5', 'tls' => 'required', 'confirm_replace' => '1']);
        $call = $this->lastCall('ftp.configure');
        self::assertSame([40000, 40100, 'required', true], [$call['passive_min'], $call['passive_max'], $call['tls'], $call['confirm_replace_service']]);
        self::assertStringContainsString('Firewall', (string) $this->flash('success'));
        $this->post('/ftp/configure', ['passive_min' => '100', 'passive_max' => '40100', 'max_clients' => '50', 'max_per_ip' => '5']);
        self::assertArrayHasKey('passive_min', (array) $this->flash('errors'));
        self::assertSame(1, $this->calls('ftp.configure'));
    }
}
