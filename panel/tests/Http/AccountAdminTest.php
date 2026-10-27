<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\PlanLimits;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/** Limit overrides, plan changes, bandwidth over the limit, bulk termination. */
final class AccountAdminTest extends HttpTestCase
{
    private Connection $db;
    private int $cust;
    private int $reseller;
    private int $sub;
    private int $small;
    private int $big;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->reseller = $this->seedUser('resell', 'reseller');
        $this->sub = $this->seedUser('sub', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->db->run('UPDATE users SET parent_id = ? WHERE id = ?', [$this->reseller, $this->sub]);
        $this->db->run("INSERT INTO plans (name, disk_quota_mb, bandwidth_mb, max_websites, created_at, updated_at) VALUES ('Small', 10240, 1000, 1, 'x', 'x')");
        $this->small = (int) $this->db->lastInsertId();
        $this->db->run("INSERT INTO plans (name, disk_quota_mb, bandwidth_mb, max_websites, created_at, updated_at) VALUES ('Big', 51200, 0, 10, 'x', 'x')");
        $this->big = (int) $this->db->lastInsertId();
        $this->db->run('UPDATE users SET plan_id = ? WHERE id IN (?, ?)', [$this->small, $this->cust, $this->sub]);
        foreach (['sites.apply' => ['changed' => []], 'sites.delete' => ['removed' => true], 'cron.apply' => ['ok' => true], 'stats.enable' => ['ok' => true], 'mail.mailbox_delete' => ['ok' => true], 'mail.alias_delete' => ['ok' => true], 'mail.domain_remove' => ['ok' => true], 'db.drop' => ['ok' => true], 'db.user_drop' => ['ok' => true], 'dns.zone_delete' => ['ok' => true]] as $a => $r) {
            $this->agent->succeed($a, $r);
        }
    }

    private function site(int $owner, string $domain): int
    {
        $users = $this->app->container->get(UserRepository::class);
        return $this->app->container->get(WebsiteService::class)->create(['domain' => $domain, 'kind' => 'static'], (array) $users->find($owner));
    }

    private function switchTo(string $username): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($username);
    }

    public function testOverridesChangeTheLimitsButNotThePlan(): void
    {
        $this->login('admin');
        self::assertStringContainsString('id="account-limits"', $this->request('GET', '/users/' . $this->cust . '/edit')->body());
        $this->post('/users/' . $this->cust . '/limits', ['limits' => ['disk_quota_mb' => '25600', 'max_websites' => '2', 'bandwidth_mb' => '']]);
        $limits = $this->app->container->get(PlanLimits::class);
        self::assertSame(['disk_quota_mb' => 25600, 'max_websites' => 2], $limits->overrides($this->cust));
        self::assertSame(25600, $limits->value($this->cust, 'disk_quota_mb'));
        self::assertSame(1000, $limits->value($this->cust, 'bandwidth_mb'), 'empty: the plan\'s value');
        self::assertSame(10240, (int) $this->db->scalar('SELECT disk_quota_mb FROM plans WHERE id = ?', [$this->small]), 'the plan is unchanged');
        self::assertSame(1, $limits->value($this->sub, 'max_websites'), 'other accounts on the plan are unaffected');
        $this->site($this->cust, 'one.test');
        $this->site($this->cust, 'two.test'); // allowed by the override
        $this->expectExceptionMessage('at most 2 websites');
        try {
            $this->site($this->cust, 'three.test');
        } finally {
            $audit = $this->db->fetch("SELECT details FROM audit_logs WHERE action = 'account.limits'");
            self::assertStringContainsString('25600', (string) $audit['details']);
        }
    }

    public function testInvalidOverridesAndNonAdministratorsAreRefused(): void
    {
        $this->login('admin');
        $this->post('/users/' . $this->cust . '/limits', ['limits' => ['disk_quota_mb' => '-5']]);
        self::assertStringContainsString('whole number', (string) $this->flash('error'));
        $this->post('/users/' . $this->cust . '/limits', ['limits' => ['disk_quota_mb' => '1e3']]);
        self::assertSame([], $this->app->container->get(PlanLimits::class)->overrides($this->cust));

        $this->switchTo('resell');
        $this->post('/users/' . $this->sub . '/limits', ['limits' => ['max_websites' => '50']]);
        self::assertSame([], $this->app->container->get(PlanLimits::class)->overrides($this->sub));
        self::assertStringNotContainsString('id="limits-modal"', $this->request('GET', '/users/' . $this->sub . '/edit')->body());
    }

    public function testPlanChangeShowsTheDifferencesFirst(): void
    {
        $this->site($this->cust, 'one.test');
        $this->db->run("INSERT INTO plans (name, max_websites, created_at, updated_at) VALUES ('Zero', 0, 'x', 'x')");
        $this->login('admin');
        $page = $this->request('GET', '/users/' . $this->cust . '/plan', [], [], '', ['plan' => (string) $this->big])->body();
        foreach (['id="plan-diff"', 'From “Small” to “Big”', '51,200', 'Change to “Big”'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        $this->post('/users/' . $this->cust . '/plan', ['plan_id' => (string) $this->big]);
        self::assertSame($this->big, (int) $this->db->scalar('SELECT plan_id FROM users WHERE id = ?', [$this->cust]));
        self::assertSame('Big', json_decode((string) $this->db->scalar("SELECT details FROM audit_logs WHERE action = 'account.plan_change'"), true)['to']);

        // A downgrade below current use is flagged; nothing is removed.
        $this->db->run("UPDATE plans SET max_websites = 0 WHERE id = ?", [$this->small]);
        $this->db->run("INSERT INTO plans (name, max_websites, created_at, updated_at) VALUES ('Tiny', 0, 'x', 'x')");
        $this->db->run("UPDATE plans SET max_websites = 1, disk_quota_mb = 1 WHERE name = 'Tiny'");
        $this->db->run("INSERT INTO website_usage (website_id, bytes, files, measured_at) SELECT id, 5 * 1048576, 1, 'x' FROM websites");
        $tiny = (int) $this->db->scalar("SELECT id FROM plans WHERE name = 'Tiny'");
        $page = $this->request('GET', '/users/' . $this->cust . '/plan', [], [], '', ['plan' => (string) $tiny])->body();
        self::assertStringContainsString('Already over the new limit', $page);
    }

    public function testResellersChangeTheirCustomersPlansToTheirOwnPlansOnly(): void
    {
        $this->db->run("INSERT INTO plans (owner_id, name, created_at, updated_at) VALUES (?, 'Resold', 'x', 'x')", [$this->reseller]);
        $resold = (int) $this->db->lastInsertId();
        $this->login('resell');
        $this->post('/users/' . $this->sub . '/plan', ['plan_id' => (string) $this->big]);
        self::assertSame($this->small, (int) $this->db->scalar('SELECT plan_id FROM users WHERE id = ?', [$this->sub]));
        $this->post('/users/' . $this->sub . '/plan', ['plan_id' => (string) $resold]);
        self::assertSame($resold, (int) $this->db->scalar('SELECT plan_id FROM users WHERE id = ?', [$this->sub]));
        $this->post('/users/' . $this->cust . '/plan', ['plan_id' => (string) $resold]);
        self::assertSame($this->small, (int) $this->db->scalar('SELECT plan_id FROM users WHERE id = ?', [$this->cust]), 'not the reseller\'s customer');
    }

    public function testBandwidthExceedersAndReset(): void
    {
        $a = $this->site($this->cust, 'one.test');
        $b = $this->site($this->sub, 'sub.test');
        $month = gmdate('Y-m');
        $this->db->run("INSERT INTO website_usage (website_id, bytes, files, measured_at, bw_month, bw_bytes) VALUES (?, 0, 0, 'x', ?, ?), (?, 0, 0, 'x', ?, ?)", [$a, $month, 1500 * 1048576, $b, $month, 200 * 1048576]);
        $this->login('admin');
        $page = $this->request('GET', '/accounts/bandwidth')->body();
        self::assertStringContainsString('cust', $page);
        self::assertStringContainsString('1,500', $page);
        self::assertStringContainsString('>500<', $page, 'over by 500 MB');
        self::assertStringNotContainsString('>sub<', $page, 'under the limit');
        self::assertStringContainsString('>sub<', $this->request('GET', '/accounts/bandwidth', [], [], '', ['show' => 'all'])->body());

        // An override lifts the account out of the list.
        $this->post('/users/' . $this->cust . '/limits', ['limits' => ['bandwidth_mb' => '2000']]);
        self::assertStringNotContainsString('1,500', $this->request('GET', '/accounts/bandwidth')->body());

        $this->post('/users/' . $this->cust . '/bandwidth/reset', ['return_to' => 'bandwidth']);
        self::assertStringContainsString('1,500 MB were counted', (string) $this->flash('success'));
        self::assertSame(0, (int) $this->db->scalar('SELECT bw_bytes FROM website_usage WHERE website_id = ?', [$a]));
        self::assertSame(200 * 1048576, (int) $this->db->scalar('SELECT bw_bytes FROM website_usage WHERE website_id = ?', [$b]), 'other accounts untouched');
        self::assertSame('account.bandwidth_reset', $this->db->scalar("SELECT action FROM audit_logs WHERE action = 'account.bandwidth_reset'"));

        $this->switchTo('resell');
        $this->post('/users/' . $this->sub . '/bandwidth/reset');
        self::assertSame(200 * 1048576, (int) $this->db->scalar('SELECT bw_bytes FROM website_usage WHERE website_id = ?', [$b]), 'administrators only');
    }

    public function testBulkTerminationListsEverythingAndNeedsTheWord(): void
    {
        $a = $this->site($this->cust, 'one.test');
        $this->db->run("INSERT INTO mail_domains (server_id, domain, website_id, owner_id, dkim_selector, dkim_txt, created_at) VALUES (1, 'one.test', ?, ?, 's', 't', 'x')", [$a, $this->cust]);
        $dom = (int) $this->db->lastInsertId();
        $this->db->run("INSERT INTO mail_mailboxes (domain_id, local, quota_mb, created_at) VALUES (?, 'info', 0, 'x')", [$dom]);
        $this->db->run("INSERT INTO databases (server_id, owner_id, name, created_at) VALUES (1, ?, 'one_db', 'x')", [$this->cust]);
        $this->db->run("INSERT INTO database_users (owner_id, username, host, created_at) VALUES (?, 'one_u', 'localhost', 'x')", [$this->cust]);
        $this->db->run("INSERT INTO dns_zones (server_id, name, owner_id, primary_ns, hostmaster, created_at, updated_at) VALUES (1, 'one.test', ?, 'ns1.x.test', 'h.x.test', 'x', 'x')", [$this->cust]);
        $this->login('admin');
        $admin = (int) $this->db->scalar("SELECT id FROM users WHERE username = 'admin'");
        self::assertStringContainsString('Terminate selected', $this->request('GET', '/users')->body());

        $page = $this->post('/accounts/terminate/confirm', ['ids' => [(string) $this->cust, (string) $this->reseller, (string) $admin]])->body();
        foreach (['one.test', 'Type <code>TERMINATE</code>', 'Has 1 customer(s) not selected', 'Not a hosting account'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertSame(0, $this->calls('sites.delete'));

        $this->post('/accounts/terminate', ['ids' => [(string) $this->cust], 'count' => '1', 'confirm' => 'terminate']);
        self::assertSame(0, $this->calls('sites.delete'), 'the word must match exactly');
        self::assertNotNull($this->db->scalar('SELECT id FROM users WHERE id = ?', [$this->cust]));

        $res = $this->post('/accounts/terminate', ['ids' => [(string) $this->cust, (string) $this->reseller, (string) $admin], 'count' => '3', 'confirm' => 'TERMINATE'])->body();
        self::assertStringContainsString('Terminated: 1 website(s), 1 mailbox(es), 1 database(s), 1 DNS zone(s).', $res);
        self::assertStringContainsString('still has 1 customer(s)', $res);
        self::assertNull($this->db->scalar('SELECT id FROM users WHERE id = ?', [$this->cust]));
        self::assertNotNull($this->db->scalar('SELECT id FROM users WHERE id = ?', [$this->reseller]), 'skipped');
        self::assertNotNull($this->db->scalar('SELECT id FROM users WHERE id = ?', [$admin]), 'staff are never terminated here');
        foreach (['sites.delete', 'mail.mailbox_delete', 'mail.domain_remove', 'db.drop', 'db.user_drop', 'dns.zone_delete'] as $action) {
            self::assertSame(1, $this->calls($action), $action);
        }
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM websites WHERE domain = ?', ['one.test']));
    }

    public function testAFailedTerminationKeepsTheAccountAndContinuesWithTheOthers(): void
    {
        $this->site($this->cust, 'one.test');
        $this->site($this->reseller, 'r.test');
        $this->site($this->sub, 'sub.test');
        $this->agent->fail('sites.delete', 500, 'failed', 'nginx -t failed');
        $this->login('admin');
        $res = $this->post('/accounts/terminate', ['ids' => [(string) $this->cust, (string) $this->reseller, (string) $this->sub], 'count' => '3', 'confirm' => 'TERMINATE'])->body();
        self::assertSame(3, substr_count($res, 'Stopped: nginx -t failed'));
        self::assertSame(3, (int) $this->db->scalar('SELECT COUNT(*) FROM users WHERE id IN (?, ?, ?)', [$this->cust, $this->reseller, $this->sub]));
        self::assertSame(3, (int) $this->db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'account.terminate' AND result = 'failure'"));

        $this->switchTo('resell');
        $this->post('/accounts/terminate', ['ids' => [(string) $this->sub], 'count' => '1', 'confirm' => 'TERMINATE']);
        self::assertNotNull($this->db->scalar('SELECT id FROM users WHERE id = ?', [$this->sub]), 'administrators only');
    }
}
