<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\RoleRepository;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\MonitoringService;
use WhaCP\Services\PlanLimits;
use WhaCP\Services\WebsiteService;
use WhaCP\Support\UserFacingException;
use WhaCP\Tests\Support\HttpTestCase;

final class PlansAndResellersTest extends HttpTestCase
{
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->db = $this->app->container->get(Connection::class);
    }

    private function as(string $user): void
    {
        $this->session = new \WhaCP\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login($user);
    }

    private function roleId(string $name): int
    {
        return (int) $this->app->container->get(RoleRepository::class)->findByName($name)['id'];
    }

    public function testResellersOnlyManageTheirOwnCustomers(): void
    {
        $this->seedUser('admin', 'admin');
        $reseller = $this->seedUser('resell', 'reseller');
        $other = $this->seedUser('stranger', 'customer');

        $this->as('resell');
        $page = $this->request('GET', '/users')->body();
        self::assertStringNotContainsString('stranger', $page);
        self::assertStringNotContainsString('>admin<', $page);
        self::assertSame(303, $this->request('GET', '/users/' . $other . '/edit')->status());
        $this->post('/users/' . $other, ['email' => 'hijack@example.test', 'role_id' => (string) $this->roleId('customer'), 'is_active' => '1']);
        self::assertSame('stranger@example.test', $this->db->scalar('SELECT email FROM users WHERE id = ?', [$other]));
        $this->post('/users/' . $other . '/delete');
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM users WHERE id = ?', [$other]));

        // A reseller creates customers only (never resellers or admins).
        $this->post('/users', ['username' => 'sub-reseller', 'email' => 'sr@example.test', 'role_id' => (string) $this->roleId('reseller'), 'password' => 'a-long-test-password', 'password_confirmation' => 'a-long-test-password', 'is_active' => '1']);
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM users WHERE username = 'sub-reseller'"));
        $this->post('/users', ['username' => 'client1', 'email' => 'c1@example.test', 'role_id' => (string) $this->roleId('customer'), 'password' => 'a-long-test-password', 'password_confirmation' => 'a-long-test-password', 'is_active' => '1']);
        self::assertSame($reseller, (int) $this->db->scalar("SELECT parent_id FROM users WHERE username = 'client1'"));
        self::assertStringContainsString('client1', $this->request('GET', '/users')->body());
    }

    public function testPlansAssignmentAndAggregateLimits(): void
    {
        $admin = $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->post('/plans', ['name' => 'Reseller S', 'max_websites' => '2', 'max_customers' => '1', 'max_cron_jobs' => '1']);
        $resellerPlan = (int) $this->db->scalar("SELECT id FROM plans WHERE name = 'Reseller S'");
        $reseller = $this->seedUser('resell', 'reseller');
        $this->post('/users/' . $reseller, ['email' => 'resell@example.test', 'role_id' => (string) $this->roleId('reseller'), 'plan_id' => (string) $resellerPlan, 'is_active' => '1']);
        self::assertSame($resellerPlan, (int) $this->db->scalar('SELECT plan_id FROM users WHERE id = ?', [$reseller]));

        $this->as('resell');
        // Resellers cannot assign the administrator's plans; they make their own.
        $this->post('/users', ['username' => 'client1', 'email' => 'c1@example.test', 'role_id' => (string) $this->roleId('customer'), 'plan_id' => (string) $resellerPlan, 'password' => 'a-long-test-password', 'password_confirmation' => 'a-long-test-password', 'is_active' => '1']);
        self::assertArrayHasKey('plan_id', (array) $this->flash('errors'));
        $this->post('/plans', ['name' => 'Basic', 'max_websites' => '5', 'max_customers' => '99']);
        $basic = (int) $this->db->scalar("SELECT id FROM plans WHERE name = 'Basic'");
        self::assertSame(0, (int) $this->db->scalar('SELECT max_customers FROM plans WHERE id = ?', [$basic]), 'customers of resellers cannot resell');
        self::assertStringNotContainsString('Reseller S', $this->request('GET', '/plans')->body());
        $this->post('/users', ['username' => 'client1', 'email' => 'c1@example.test', 'role_id' => (string) $this->roleId('customer'), 'plan_id' => (string) $basic, 'password' => 'a-long-test-password', 'password_confirmation' => 'a-long-test-password', 'is_active' => '1']);
        $client = (int) $this->db->scalar("SELECT id FROM users WHERE username = 'client1'");
        self::assertSame($basic, (int) $this->db->scalar('SELECT plan_id FROM users WHERE id = ?', [$client]));
        // max_customers = 1 on the reseller's plan.
        $this->post('/users', ['username' => 'client2', 'email' => 'c2@example.test', 'role_id' => (string) $this->roleId('customer'), 'password' => 'a-long-test-password', 'password_confirmation' => 'a-long-test-password', 'is_active' => '1']);
        self::assertStringContainsString('at most 1 customers', (string) $this->flash('error'));

        // Websites: the reseller's plan (2) caps reseller + customers together,
        // even though the customer's own plan would allow 5.
        $sites = $this->app->container->get(WebsiteService::class);
        $users = $this->app->container->get(UserRepository::class);
        $sites->create(['domain' => 'r1.test', 'kind' => 'static'], (array) $users->find($reseller));
        $sites->create(['domain' => 'c1.test', 'kind' => 'static'], (array) $users->find($client));
        try {
            $sites->create(['domain' => 'c2.test', 'kind' => 'static'], (array) $users->find($client));
            self::fail('aggregate limit not enforced');
        } catch (UserFacingException $e) {
            self::assertStringContainsString("provider's plan", $e->getMessage());
        }

        // Cron jobs count against the plans as well.
        $this->agent->succeed('cron.apply', ['changed' => true]);
        $siteId = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'c1.test'");
        $cron = $this->app->container->get(\WhaCP\Services\CronService::class);
        $site = $this->app->container->get(\WhaCP\Repositories\WebsiteRepository::class)->find(1, $siteId, null);
        $cron->save($site, null, ['schedule' => '@daily', 'command' => 'true']);
        $this->expectException(UserFacingException::class);
        $cron->save($site, null, ['schedule' => '@daily', 'command' => 'true']);
    }

    public function testDiskQuotaAlert(): void
    {
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $this->db->run("INSERT INTO plans (name, disk_quota_mb, created_at, updated_at) VALUES ('Small', 1, 'x', 'x')");
        $this->db->run('UPDATE users SET plan_id = (SELECT id FROM plans) WHERE id = ?', [$cust]);
        $site = $this->app->container->get(WebsiteService::class)->create(['domain' => 'big.test', 'kind' => 'static'], (array) $this->app->container->get(UserRepository::class)->find($cust));
        $this->agent->succeed('system.info', ['system' => ['cpu' => ['cores' => 1]]]);
        $this->agent->succeed('system.metrics', ['cpu_percent' => 1, 'memory' => ['used_percent' => 1], 'load' => ['load1' => 0], 'disks' => [], 'network' => []]);
        $this->agent->succeed('services.status', []);
        $this->agent->succeed('discovery.certificates', []);
        $this->agent->succeed('sites.usage', [['domain' => 'big.test', 'bytes' => 5 * 1048576, 'files' => 10]]);
        $mon = $this->app->container->get(MonitoringService::class);
        $mon->run();
        self::assertSame(5 * 1048576, (int) $this->db->scalar('SELECT bytes FROM website_usage WHERE website_id = ?', [$site]));
        self::assertStringContainsString('cust uses 5 MB of a 1 MB disk quota', (string) $this->db->scalar("SELECT message FROM alerts WHERE rule = 'quota'"));
        $usage = $this->app->container->get(PlanLimits::class)->usage($cust);
        self::assertSame(['label' => 'Disk (MB)', 'used' => 5, 'limit' => 1], end($usage));
        $calls = $this->calls('sites.usage');
        $mon->run();
        self::assertSame($calls, $this->calls('sites.usage'), 'measured at most hourly');
    }
}
