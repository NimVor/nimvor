<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\ResourceLimitService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

final class ResourceLimitTest extends HttpTestCase
{
    private Connection $db;
    private int $cust;
    private int $plan;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('limits.status', ['accounts' => [], 'cgroup_v2' => true]);
        $this->agent->succeed('limits.set', ['slice' => 'x', 'users' => [], 'changed' => [], 'warnings' => []]);
        $this->agent->succeed('sites.apply', ['domain' => 'x', 'changed' => [], 'user' => ['name' => 'x', 'uid' => 1500, 'gid' => 1500]]);
        $this->agent->succeed('cron.apply', ['changed' => false]);
        $this->db->run("INSERT INTO plans (name, description, created_at, updated_at) VALUES ('Basic', '', '2026-01-01', '2026-01-01')");
        $this->plan = $this->db->lastInsertId();
        $this->db->run('UPDATE users SET plan_id = ? WHERE id = ?', [$this->plan, $this->cust]);
        $users = $this->app->container->get(UserRepository::class);
        $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'static', 'system_user' => 'shopuser'], (array) $users->find($this->cust));
    }

    public function testPlanLimitsApplyToTheAccount(): void
    {
        $this->login('admin');
        // No limits yet: nothing is sent.
        self::assertSame(0, $this->calls('limits.set'));
        $this->post('/plans', ['plan_id' => (string) $this->plan, 'name' => 'Basic', 'cpu_percent' => '5']);
        self::assertStringContainsString('10–12800', (string) ($this->flash('errors')['cpu_percent'] ?? ''));
        $this->post('/plans', ['plan_id' => (string) $this->plan, 'name' => 'Basic', 'cpu_percent' => '150', 'memory_mb' => '1024', 'tasks_max' => '128']);
        $call = $this->lastCall('limits.set');
        self::assertSame(['acct' . $this->cust, ['shopuser'], 150, 1024, 128, 0], [$call['account'], $call['users'], $call['cpu_percent'], $call['memory_mb'], $call['tasks_max'], $call['io_read_mbps']]);
        self::assertStringContainsString('resource limits applied to 1 account', (string) $this->flash('success'));
        // The account's website is applied again (its PHP moves into the slice).
        self::assertSame('shop.test', $this->lastCall('sites.apply')['domain']);

        // Applied state matches: nothing more is sent.
        $this->agent->succeed('limits.status', ['accounts' => ['acct' . $this->cust => ['limits' => ['cpu_percent' => 150, 'memory_mb' => 1024, 'tasks_max' => 128, 'io_read_mbps' => 0, 'io_write_mbps' => 0], 'users' => ['shopuser'], 'usage' => ['active' => true, 'memory_bytes' => 536870912, 'tasks' => 7, 'cpu_throttled_usec' => 5000000, 'oom_kills' => 0]]], 'cgroup_v2' => true]);
        $n = $this->calls('limits.set');
        self::assertSame([], $this->app->container->get(ResourceLimitService::class)->reconcile());
        self::assertSame($n, $this->calls('limits.set'));
        $page = $this->request('GET', '/limits')->body();
        foreach (['cust', 'CPU 150%', '1 GB memory', '512 MB (50%)', 'Applied'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }

        // Plan without limits (downgrade to unlimited): the account's limits are removed.
        $this->post('/plans', ['plan_id' => (string) $this->plan, 'name' => 'Basic', 'cpu_percent' => '0', 'memory_mb' => '0', 'tasks_max' => '0']);
        $call = $this->lastCall('limits.set');
        self::assertSame(['acct' . $this->cust, 0, 0], [$call['account'], $call['cpu_percent'], $call['memory_mb']]);
    }

    public function testResellerPlanCapsItsCustomers(): void
    {
        $res = $this->seedUser('res', 'reseller');
        $this->db->run("INSERT INTO plans (name, description, created_at, updated_at, cpu_percent, memory_mb) VALUES ('Reseller', '', '2026-01-01', '2026-01-01', 200, 512)");
        $rp = $this->db->lastInsertId();
        $this->db->run('UPDATE users SET plan_id = ? WHERE id = ?', [$rp, $res]);
        $this->db->run('UPDATE users SET parent_id = ? WHERE id = ?', [$res, $this->cust]);
        $this->db->run('UPDATE plans SET cpu_percent = 400, memory_mb = 0, tasks_max = 64 WHERE id = ?', [$this->plan]);
        $l = $this->app->container->get(ResourceLimitService::class)->forUser($this->cust);
        self::assertSame(['cpu_percent' => 200, 'memory_mb' => 512, 'tasks_max' => 64, 'io_read_mbps' => 0, 'io_write_mbps' => 0], $l);
    }

    public function testNewWebsiteJoinsTheAccountSlice(): void
    {
        $this->db->run('UPDATE plans SET memory_mb = 512 WHERE id = ?', [$this->plan]);
        $users = $this->app->container->get(UserRepository::class);
        $this->app->container->get(WebsiteService::class)->create(['domain' => 'blog.test', 'kind' => 'static', 'system_user' => 'bloguser'], (array) $users->find($this->cust));
        $call = $this->lastCall('limits.set');
        self::assertSame(['bloguser', 'shopuser'], $call['users']);
        self::assertSame(512, $call['memory_mb']);
    }

    public function testCustomersCannotSeeOrApply(): void
    {
        $this->login('cust');
        self::assertNotSame(200, $this->request('GET', '/limits')->status());
        $n = $this->calls('limits.set');
        $this->post('/limits/apply', []);
        self::assertSame($n, $this->calls('limits.set'));
    }
}
