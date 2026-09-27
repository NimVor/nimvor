<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Http\Request;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\ApiTokenService;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/** Account suspension: sign-in, websites, cron, workers, mail, databases, SSH/FTP. */
final class SuspensionTest extends HttpTestCase
{
    private Connection $db;
    private int $cust;
    private int $reseller;
    private int $sub;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->reseller = $this->seedUser('resell', 'reseller');
        $this->sub = $this->seedUser('sub', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run('UPDATE users SET parent_id = ? WHERE id = ?', [$this->reseller, $this->sub]);
        foreach (['sites.apply' => ['changed' => []], 'cron.apply' => ['ok' => true], 'workers.apply' => ['ok' => true], 'account.suspend' => ['ok' => true], 'mail.suspend' => ['ok' => true], 'db.user_lock' => ['ok' => true]] as $a => $r) {
            $this->agent->succeed($a, $r);
        }
    }

    private function site(int $owner, string $domain): int
    {
        $users = $this->app->container->get(UserRepository::class);
        $id = $this->app->container->get(WebsiteService::class)->create(['domain' => $domain, 'kind' => 'php', 'php_version' => '8.3'], (array) $users->find($owner));
        $this->db->run("INSERT INTO cron_jobs (website_id, schedule, command, enabled, created_at, updated_at) VALUES (?, '*/5 * * * *', 'php artisan schedule:run', 1, 'x', 'x')", [$id]);
        $this->db->run("INSERT INTO mail_domains (server_id, domain, website_id, owner_id, dkim_selector, dkim_txt, created_at) VALUES (1, ?, ?, ?, 's', 't', 'x')", [$domain, $id, $owner]);
        $this->db->run("INSERT INTO database_users (owner_id, username, host, created_at) VALUES (?, ?, 'localhost', 'x')", [$owner, str_replace('.', '_', $domain)]);
        return $id;
    }

    /** @return list<array<string, mixed>> */
    private function all(string $action): array
    {
        return array_values(array_map(static fn (array $r): array => (array) json_decode((string) $r['body'], true), array_filter($this->agent->requests, static fn (array $r): bool => $r['path'] === '/v1/actions/' . $action)));
    }

    private function signIn(string $username): string
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $res = $this->post('/login', ['username' => $username, 'password' => 'a-long-test-password']);
        return (string) ($res->header('Location') === '/' ? 'ok' : $this->flash('error'));
    }

    public function testSuspendingACustomerTurnsEverythingOffAndBackOn(): void
    {
        $site = $this->site($this->cust, 'shop.test');
        $token = $this->app->container->get(ApiTokenService::class)->create($this->cust, ['name' => 't', 'ability' => 'read', 'expires_days' => 30]);
        $this->agent->requests = [];
        $this->login('admin');
        $this->post('/users/' . $this->cust . '/suspend', ['reason' => 'Unpaid invoice']);
        self::assertStringContainsString('suspended', (string) $this->flash('success'));
        $user = $this->db->fetch('SELECT suspended_at, suspend_reason FROM users WHERE id = ?', [$this->cust]);
        self::assertNotNull($user['suspended_at']);
        self::assertSame('Unpaid invoice', $user['suspend_reason']);

        $apply = $this->lastCall('sites.apply');
        self::assertSame(['shop.test', false], [$apply['domain'], $apply['enabled']]);
        self::assertSame(1, (int) $this->db->scalar('SELECT enabled FROM websites WHERE id = ?', [$site]), 'the website\'s own setting is kept');
        self::assertSame('suspended', $this->db->scalar('SELECT status FROM websites WHERE id = ?', [$site]));
        self::assertFalse($this->lastCall('cron.apply')['jobs'][0]['enabled']);
        self::assertSame(1, (int) $this->db->scalar('SELECT enabled FROM cron_jobs'), 'the job\'s own setting is kept');
        $system = (string) $this->db->scalar('SELECT system_user FROM websites WHERE id = ?', [$site]);
        self::assertSame(['user' => $system, 'suspended' => true], $this->lastCall('account.suspend'));
        self::assertSame(['domain' => 'shop.test', 'suspended' => true], $this->lastCall('mail.suspend'));
        self::assertSame(['user' => 'shop_test', 'host' => 'localhost', 'locked' => true], $this->lastCall('db.user_lock'));
        $audit = $this->db->fetch("SELECT result, details FROM audit_logs WHERE action = 'account.suspend'");
        self::assertSame('success', $audit['result']);

        // The customer cannot sign in or use an API token.
        self::assertStringContainsString('suspended', $this->signIn('cust'));
        $res = $this->app->handle(new Request('GET', '/api/v1/me', [], [], ['REMOTE_ADDR' => '192.0.2.50'], ['authorization' => 'Bearer ' . $token]));
        self::assertSame(401, $res->status());

        $this->signIn('admin');
        self::assertStringContainsString('Account suspended', $this->request('GET', '/sites/' . $site)->body());
        self::assertStringContainsString('Unsuspend', $this->request('GET', '/users/' . $this->cust . '/edit')->body());
        $this->post('/users/' . $this->cust . '/unsuspend');
        self::assertNull($this->db->scalar('SELECT suspended_at FROM users WHERE id = ?', [$this->cust]));
        self::assertTrue($this->lastCall('sites.apply')['enabled']);
        self::assertTrue($this->lastCall('cron.apply')['jobs'][0]['enabled']);
        self::assertFalse($this->lastCall('account.suspend')['suspended']);
        self::assertFalse($this->lastCall('mail.suspend')['suspended']);
        self::assertFalse($this->lastCall('db.user_lock')['locked']);
        self::assertSame('active', $this->db->scalar('SELECT status FROM websites WHERE id = ?', [$site]));
        self::assertSame('ok', $this->signIn('cust'));
    }

    public function testSuspendingAResellerSuspendsItsCustomers(): void
    {
        $this->site($this->sub, 'sub.test');
        $this->login('admin');
        $this->post('/users/' . $this->reseller . '/suspend', ['reason' => '']);
        self::assertFalse($this->lastCall('sites.apply')['enabled'], 'the customer\'s website is off');
        self::assertStringContainsString('suspended', $this->signIn('sub'));
        self::assertStringContainsString('suspended', $this->signIn('resell'));
        self::assertNull($this->db->scalar('SELECT suspended_at FROM users WHERE id = ?', [$this->sub]), 'suspended through the reseller only');

        // A customer suspended on its own stays suspended when the reseller resumes.
        $this->signIn('admin');
        $this->post('/users/' . $this->sub . '/suspend', ['reason' => 'abuse']);
        $this->post('/users/' . $this->reseller . '/unsuspend');
        self::assertStringContainsString('suspended', $this->signIn('sub'));
        self::assertSame('ok', $this->signIn('resell'));
        self::assertFalse($this->lastCall('sites.apply')['enabled']);
    }

    public function testOnlyAdministratorsSuspendAndOnlyHostingAccounts(): void
    {
        $this->site($this->sub, 'sub.test');
        $this->login('resell');
        $this->post('/users/' . $this->sub . '/suspend', ['reason' => 'x']);
        self::assertNull($this->db->scalar('SELECT suspended_at FROM users WHERE id = ?', [$this->sub]));
        self::assertSame(0, $this->calls('account.suspend'));

        $this->signIn('admin');
        $admin = (int) $this->db->scalar("SELECT id FROM users WHERE username = 'admin'");
        $this->post('/users/' . $admin . '/suspend', ['reason' => 'x']);
        self::assertStringContainsString('Only hosting accounts', (string) $this->flash('error'));
        self::assertNull($this->db->scalar('SELECT suspended_at FROM users WHERE id = ?', [$admin]));
        self::assertStringNotContainsString('Suspend account', $this->request('GET', '/users/' . $admin . '/edit')->body());
    }

    public function testAFailedServiceIsReportedAndCanBeRetried(): void
    {
        $this->site($this->cust, 'shop.test');
        $this->agent->fail('mail.suspend', 500, 'failed', 'doveconf failed');
        $this->login('admin');
        $this->post('/users/' . $this->cust . '/suspend', ['reason' => '']);
        $error = (string) $this->flash('error');
        self::assertStringContainsString('email for shop.test: doveconf failed', $error);
        self::assertNotNull($this->db->scalar('SELECT suspended_at FROM users WHERE id = ?', [$this->cust]), 'sign-in is blocked anyway');
        self::assertSame('failure', $this->db->scalar("SELECT result FROM audit_logs WHERE action = 'account.suspend'"));
        $this->agent->succeed('mail.suspend', ['ok' => true]);
        $this->post('/users/' . $this->cust . '/suspend', ['reason' => '']);
        self::assertStringContainsString('suspended', (string) $this->flash('success'));
        self::assertCount(2, array_filter($this->all('mail.suspend'), static fn (array $c): bool => $c['suspended'] === true));
    }

    public function testAWebsiteRunByAnAccountWhaCPDidNotCreateIsSkippedForSshAndFtp(): void
    {
        $this->site($this->cust, 'shop.test');
        $this->agent->fail('account.suspend', 400, 'invalid_params', 'www-data is not a website account');
        $this->login('admin');
        $this->post('/users/' . $this->cust . '/suspend', ['reason' => '']);
        self::assertStringContainsString('suspended', (string) $this->flash('success'));
    }
}
