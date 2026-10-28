<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Services\DiskQuotaService;
use NimVor\Services\PlanLimits;
use NimVor\Tests\Support\HttpTestCase;

/** Plans' disk space enforced with the agent's user quotas. */
final class DiskQuotaTest extends HttpTestCase
{
    private Connection $db;
    private int $cust;
    private int $reseller;
    private int $sub;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->reseller = $this->seedUser('resell', 'reseller');
        $this->sub = $this->seedUser('sub', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run('UPDATE users SET parent_id = ? WHERE id = ?', [$this->reseller, $this->sub]);
        $this->db->run("INSERT INTO plans (name, disk_quota_mb, created_at, updated_at) VALUES ('Small', 100, 'x', 'x')");
        $this->db->run('UPDATE users SET plan_id = ? WHERE id IN (?, ?)', [(int) $this->db->lastInsertId(), $this->cust, $admin]);
        foreach ([['a.test', 'web1', $this->cust], ['b.test', 'web2', $this->cust], ['sub.test', 'web3', $this->sub], ['admin.test', 'web4', $admin]] as [$d, $u, $o]) {
            $this->db->run("INSERT INTO websites (server_id, domain, kind, php_version, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, ?, 'php', '8.3', ?, ?, ?, ?, 'active', 'x', 'x')", [$d, '/home/nimvor/' . $d, '/home/nimvor/' . $d . '/public_html', $u, $o]);
        }
    }

    private function service(): DiskQuotaService
    {
        return $this->app->container->get(DiskQuotaService::class);
    }

    /** @return list<array<string, mixed>> */
    private function agentCalls(string $action): array
    {
        $out = [];
        foreach ($this->agent->requests as $r) {
            if ($r['path'] === '/v1/actions/' . $action) {
                $out[] = (array) json_decode((string) $r['body'], true);
            }
        }
        return $out;
    }

    public function testTheLimitIsSharedAndNeverExceededTogether(): void
    {
        $mb = 1024;
        // One user: exactly the limit.
        self::assertSame(['u' => 100 * $mb], DiskQuotaService::split(['u' => 1], ['u' => 30 * $mb], [1 => 100 * $mb], [1 => null]));
        // Two users: each may grow by half of what is left; together at most the limit.
        $two = DiskQuotaService::split(['u' => 1, 'v' => 1], ['u' => 60 * $mb, 'v' => 10 * $mb], [1 => 100 * $mb], [1 => null]);
        self::assertSame(['u' => 75 * $mb, 'v' => 25 * $mb], $two);
        // Over the limit already: no further growth, never 0 (= unlimited).
        self::assertSame(['u' => 120 * $mb, 'e' => 1], DiskQuotaService::split(['u' => 1, 'e' => 1], ['u' => 120 * $mb, 'e' => 0], [1 => 100 * $mb], [1 => null]));
        // A reseller's limit covers its customers; the smaller part wins.
        $r = DiskQuotaService::split(['c' => 5, 'r' => 4], ['c' => 0, 'r' => 0], [5 => 100 * $mb, 4 => 50 * $mb], [5 => 4, 4 => null]);
        self::assertSame(['c' => 25 * $mb, 'r' => 25 * $mb], $r);
        // No limit anywhere, or no owner: none.
        self::assertSame(['x' => 0, 'y' => 0], DiskQuotaService::split(['x' => 7, 'y' => null], [], [], [7 => null]));
    }

    public function testReconcileSetsWhatChangedAndCountsTheQuotaUse(): void
    {
        $this->agent->succeed('quota.status', ['state' => 'on', 'mount_point' => '/', 'fstype' => 'ext4', 'path' => '/home/nimvor']);
        $this->agent->succeed('quota.report', ['mount_point' => '/', 'users' => [
            'web1' => ['used_kb' => 40 * 1024, 'hard_kb' => 0], 'web2' => ['used_kb' => 20 * 1024, 'hard_kb' => 0],
            'web3' => ['used_kb' => 5, 'hard_kb' => 0], 'web4' => ['used_kb' => 999999, 'hard_kb' => 0],
        ]]);
        $this->agent->succeed('quota.set', ['set' => ['web1', 'web2'], 'failed' => []]);
        $lines = $this->service()->reconcile();
        self::assertSame(['web1', 'web2', 'web3', 'web4'], $this->agentCalls('quota.report')[0]['users']);
        $set = $this->agentCalls('quota.set');
        self::assertCount(1, $set);
        // 100 MB, 60 used by two users: 20 MB more each. Others unlimited: unchanged.
        self::assertSame(['web1' => 60 * 1024, 'web2' => 40 * 1024], $set[0]['limits']);
        self::assertCount(2, $lines);
        self::assertSame(60 * 1024, (int) $this->db->scalar("SELECT hard_kb FROM disk_quotas WHERE system_user = 'web1'"));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'quota.apply'"));
        // The plan's usage now reads the quota (what is enforced).
        self::assertSame(60, $this->app->container->get(PlanLimits::class)->count('disk_mb', [$this->cust]));
    }

    public function testNothingHappensWhileQuotasAreOff(): void
    {
        $this->agent->succeed('quota.status', ['state' => 'off', 'mount_point' => '/', 'fstype' => 'ext4', 'path' => '/home/nimvor']);
        self::assertSame([], $this->service()->reconcile());
        self::assertSame([], $this->agentCalls('quota.report'));
        self::assertSame([], $this->agentCalls('quota.set'));
    }

    public function testOnlyAdministratorsTurnQuotasOn(): void
    {
        $this->agent->succeed('quota.status', ['state' => 'off', 'mount_point' => '/', 'fstype' => 'ext4', 'device' => '/dev/vda1', 'path' => '/home/nimvor']);
        $this->agent->succeed('quota.enable', ['job_id' => '0123456789abcdef', 'status' => 'queued']);
        $this->login('admin');
        $page = $this->request('GET', '/limits')->body();
        self::assertStringContainsString('id="disk-quotas"', $page);
        self::assertStringContainsString('Turn on disk quotas', $page);
        $this->post('/limits/quota/enable');
        self::assertCount(1, $this->agentCalls('quota.enable'));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'quota.enable'"));
        self::assertSame('0123456789abcdef', $this->db->scalar("SELECT job_id FROM agent_jobs WHERE kind = 'Turn on disk quotas'"));
        self::assertStringContainsString('data-nim-job-open="0123456789abcdef"', $this->request('GET', '/limits')->body());

        foreach (['resell', 'cust'] as $who) {
            $this->session = new ArraySession();
            $this->app = $this->makeApp();
            $this->login($who);
            $this->post('/limits/quota/enable');
        }
        self::assertCount(1, $this->agentCalls('quota.enable'), 'resellers and customers cannot');
    }
}
