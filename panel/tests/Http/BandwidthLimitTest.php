<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\BandwidthLimitService;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/** Monthly bandwidth of the plans is enforced: over it, websites answer 509. */
final class BandwidthLimitTest extends HttpTestCase
{
    private Connection $db;
    private int $cust;
    private int $reseller;
    private int $sub;
    private int $plan;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->reseller = $this->seedUser('resell', 'reseller');
        $this->sub = $this->seedUser('sub', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run('UPDATE users SET parent_id = ? WHERE id = ?', [$this->reseller, $this->sub]);
        $this->db->run("INSERT INTO plans (name, bandwidth_mb, created_at, updated_at) VALUES ('Small', 1000, 'x', 'x')");
        $this->plan = (int) $this->db->lastInsertId();
        $this->db->run('UPDATE users SET plan_id = ? WHERE id IN (?, ?, ?)', [$this->plan, $this->cust, $this->sub, $admin]);
        $this->agent->succeed('sites.apply', ['changed' => []]);
    }

    private function site(int $owner, string $domain, int $mb): int
    {
        $users = $this->app->container->get(UserRepository::class);
        $id = $this->app->container->get(WebsiteService::class)->create(['domain' => $domain, 'kind' => 'static', 'system_user' => str_replace('.', '', $domain)], (array) $users->find($owner));
        $this->db->run("INSERT INTO website_usage (website_id, bytes, files, measured_at, bw_month, bw_bytes) VALUES (?, 0, 0, 'x', ?, ?)", [$id, gmdate('Y-m'), $mb * 1048576]);
        return $id;
    }

    private function reconcile(): array
    {
        return $this->app->container->get(BandwidthLimitService::class)->reconcile();
    }

    /** @return list<array<string, mixed>> sites.apply calls since the last look */
    private function applies(): array
    {
        $out = [];
        foreach ($this->agent->requests as $r) {
            if ($r['path'] === '/v1/actions/sites.apply') {
                $out[] = (array) json_decode((string) $r['body'], true);
            }
        }
        $this->agent->requests = [];
        return $out;
    }

    public function testOverTheLimitBlocksUntilResetAndOnlyThen(): void
    {
        $one = $this->site($this->cust, 'one.test', 1500);
        $this->site($this->cust, 'two.test', 0);
        $this->applies();

        $lines = $this->reconcile();
        self::assertStringContainsString('cust: over its monthly bandwidth (1500 of 1000 MB)', implode("\n", $lines));
        self::assertSame(gmdate('Y-m'), $this->db->scalar('SELECT bw_blocked_month FROM users WHERE id = ?', [$this->cust]));
        $applied = $this->applies();
        self::assertSame(['one.test', 'two.test'], array_column($applied, 'domain'), 'all the account\'s websites');
        self::assertTrue($applied[0]['bandwidth_exceeded']);
        self::assertSame(2, (int) $this->db->scalar('SELECT COUNT(*) FROM websites WHERE bandwidth_applied = 1'));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'account.bandwidth_block'"));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM notifications WHERE kind = 'bandwidth' AND owner_id = ?", [$this->cust]));

        // Nothing more to do on the next pass.
        self::assertSame([], $this->reconcile());
        self::assertSame([], $this->applies());

        // What the administrator and the customer see.
        $this->login('admin');
        self::assertStringContainsString('Blocked (509)', $this->request('GET', '/accounts/bandwidth')->body());
        self::assertStringContainsString('id="bandwidth-blocked"', $this->request('GET', '/sites/' . $one)->body());

        // A reset unblocks at once.
        $this->post('/users/' . $this->cust . '/bandwidth/reset', ['return_to' => 'bandwidth']);
        self::assertSame('', $this->db->scalar('SELECT bw_blocked_month FROM users WHERE id = ?', [$this->cust]));
        $applied = $this->applies();
        self::assertCount(2, $applied);
        self::assertArrayNotHasKey('bandwidth_exceeded', $applied[0]);
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM websites WHERE bandwidth_applied = 1'));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'account.bandwidth_unblock'"));
        self::assertStringNotContainsString('id="bandwidth-blocked"', $this->request('GET', '/sites/' . $one)->body());
    }

    public function testARaisedLimitOrANewMonthUnblocks(): void
    {
        $this->site($this->cust, 'one.test', 1500);
        $this->reconcile();
        $this->applies();

        // An override above the use.
        $this->login('admin');
        $this->post('/users/' . $this->cust . '/limits', ['limits' => ['bandwidth_mb' => '2000']]);
        self::assertSame('', $this->db->scalar('SELECT bw_blocked_month FROM users WHERE id = ?', [$this->cust]));
        self::assertCount(1, $this->applies());

        // Blocked last month: cleared, and the website serves again.
        $this->db->run("UPDATE users SET bw_blocked_month = '2000-01' WHERE id = ?", [$this->cust]);
        $this->db->run('UPDATE websites SET bandwidth_applied = 1');
        self::assertStringContainsString('websites unblocked', implode("\n", $this->reconcile()));
        $applied = $this->applies();
        self::assertCount(1, $applied);
        self::assertArrayNotHasKey('bandwidth_exceeded', $applied[0]);
    }

    public function testAResellersLimitCoversItsCustomersAndAdministratorsAreNeverBlocked(): void
    {
        $this->db->run("INSERT INTO plans (name, bandwidth_mb, created_at, updated_at) VALUES ('Reseller', 500, 'x', 'x')");
        $this->db->run('UPDATE users SET plan_id = ? WHERE id = ?', [(int) $this->db->lastInsertId(), $this->reseller]);
        $this->db->run('UPDATE plans SET bandwidth_mb = 0 WHERE id = ?', [$this->plan]);
        $this->site($this->sub, 'sub.test', 600);
        $this->site((int) $this->db->scalar("SELECT id FROM users WHERE username = 'admin'"), 'admin.test', 5000);
        $this->site($this->cust, 'other.test', 5000);
        $this->applies();

        $this->reconcile();
        self::assertSame(gmdate('Y-m'), $this->db->scalar('SELECT bw_blocked_month FROM users WHERE id = ?', [$this->reseller]));
        self::assertSame('', $this->db->scalar('SELECT bw_blocked_month FROM users WHERE id = ?', [$this->sub]), 'its own plan has no limit');
        $applied = $this->applies();
        self::assertSame(['sub.test'], array_column($applied, 'domain'), 'the reseller\'s customer is blocked; the administrator and unlimited accounts are not');
        self::assertTrue($applied[0]['bandwidth_exceeded']);
    }

    public function testAFailedApplyIsRetriedOnTheNextPass(): void
    {
        $this->site($this->cust, 'one.test', 1500);
        $this->applies();
        $this->agent->fail('sites.apply', 500, 'failed', 'nginx trouble');
        self::assertStringContainsString('one.test: not applied', implode("\n", $this->reconcile()));
        self::assertSame(0, (int) $this->db->scalar('SELECT bandwidth_applied FROM websites'));
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->reconcile();
        self::assertSame(1, (int) $this->db->scalar('SELECT bandwidth_applied FROM websites'));
    }
}
