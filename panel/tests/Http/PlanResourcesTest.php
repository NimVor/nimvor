<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\MailService;
use NimVor\Services\MonitoringService;
use NimVor\Services\PlanService;
use NimVor\Services\WebsiteService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\Tests\Support\HttpTestCase;

/**
 * Plan resources: subdomains, addon and parked domains, email accounts
 * and their quota, FTP accounts, monthly bandwidth; and what customers
 * see of them.
 */
final class PlanResourcesTest extends HttpTestCase
{
    private Connection $db;
    private int $cust;
    private int $siteId;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $this->cust = $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $plans = $this->app->container->get(PlanService::class);
        $users = $this->app->container->get(UserRepository::class);
        $planId = $plans->save((array) $users->find($admin), null, ['name' => 'Starter', 'max_websites' => '2', 'max_subdomains' => '2', 'max_addon_domains' => '2', 'max_parked_domains' => '1', 'max_email_accounts' => '1', 'max_email_quota_mb' => '500', 'max_ftp_accounts' => '1', 'bandwidth_mb' => '10', 'disk_quota_mb' => '100']);
        $this->db->run('UPDATE users SET plan_id = ? WHERE id = ?', [$planId, $this->cust]);
        $this->siteId = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'static', 'aliases' => 'www.shop.test'], (array) $users->find($this->cust));
    }

    /** @return array<string, mixed> */
    private function site(): array
    {
        return (array) $this->app->container->get(WebsiteRepository::class)->find(1, $this->siteId, null);
    }

    public function testDomainLimits(): void
    {
        $sites = $this->app->container->get(WebsiteService::class);
        $cust = (array) $this->app->container->get(UserRepository::class)->find($this->cust);
        // One parked domain (www) is used: another alias is refused.
        try {
            $sites->update($this->site(), ['aliases' => "www.shop.test\nshop2.test"], $cust);
            self::fail('second alias accepted');
        } catch (UserFacingException $e) {
            self::assertStringContainsString('parked domains', $e->getMessage());
        }
        // The primary site (shop.test) already uses one of the two website
        // slots. A subdomain does not count as a website; an addon domain does.
        $sites->createChild($this->site(), 'subdomain', ['subdomain' => 'blog'], $cust);
        $sites->createChild($this->site(), 'addon', ['domain' => 'other.test'], $cust);
        // Websites now at the cap (shop.test + other.test). A subdomain is
        // still allowed — it does not consume a website slot.
        $sites->createChild($this->site(), 'subdomain', ['subdomain' => 'docs'], $cust);
        // A further addon is refused by the website cap even though the addon
        // limit is not yet reached.
        try {
            $sites->createChild($this->site(), 'addon', ['domain' => 'third.test'], $cust);
            self::fail('addon over the website limit accepted');
        } catch (UserFacingException $e) {
            self::assertStringContainsString('websites', $e->getMessage());
        }
        // A subdomain beyond its own limit is refused by the subdomain cap.
        try {
            $sites->createChild($this->site(), 'subdomain', ['subdomain' => 'shop'], $cust);
            self::fail('subdomain over the limit accepted');
        } catch (UserFacingException $e) {
            self::assertStringContainsString('subdomains', $e->getMessage());
        }
        // Four rows (primary + two subdomains + one addon); the website count
        // enforced by the plan is two (primary + addon).
        self::assertSame(4, (int) $this->db->scalar('SELECT COUNT(*) FROM websites'));
        self::assertSame(2, $this->app->container->get(\NimVor\Services\PlanLimits::class)->count('websites', [$this->cust]));
        // A new top-level site is refused by the website cap.
        $this->expectException(UserFacingException::class);
        $sites->create(['domain' => 'second.test', 'kind' => 'static'], $cust);
    }

    public function testEmailAndFtpLimits(): void
    {
        $this->agent->succeed('mail.domain_add', ['domain' => 'shop.test', 'dkim_selector' => 's', 'dkim_txt' => 't']);
        $this->agent->succeed('mail.mailbox_save', ['ok' => true]);
        $mail = $this->app->container->get(MailService::class);
        $mail->addDomain(['website_id' => (string) $this->siteId], ['role_scope' => 'all', 'id' => 1]);
        $domain = (array) $mail->forWebsite($this->siteId);
        try {
            $mail->saveMailbox($domain, null, ['local' => 'big', 'generate' => '1', 'quota_mb' => '2000']);
            self::fail('quota over the plan accepted');
        } catch (ValidationException $e) {
            self::assertStringContainsString('500 MB', (string) $e->errors()['quota_mb']);
        }
        $mail->saveMailbox($domain, null, ['local' => 'info', 'generate' => '1', 'quota_mb' => '0']);
        self::assertSame(500, $this->lastCall('mail.mailbox_save')['quota_mb'], 'unlimited becomes the plan cap');
        try {
            $mail->saveMailbox($domain, null, ['local' => 'sales', 'generate' => '1']);
            self::fail('second mailbox accepted');
        } catch (UserFacingException $e) {
            self::assertStringContainsString('email accounts', $e->getMessage());
        }

        $user = (string) $this->db->scalar('SELECT system_user FROM websites WHERE id = ?', [$this->siteId]);
        $this->agent->succeed('ftp.status', ['installed' => true, 'configured' => true, 'active' => true, 'accounts' => [['name' => 'a@shop.test', 'user' => $user, 'dir' => '/x', 'quota_mb' => 0], ['name' => 'x@elsewhere.test', 'user' => 'someoneelse', 'dir' => '/y', 'quota_mb' => 0]]]);
        $this->agent->succeed('ftp.account_save', ['ok' => true]);
        $this->login('cust');
        $this->post('/sites/' . $this->siteId . '/ftp/accounts', ['name' => 'b', 'generate' => '1']);
        self::assertStringContainsString('FTP accounts', (string) $this->flash('error'));
        self::assertSame(0, $this->calls('ftp.account_save'), 'another account\'s FTP accounts do not count, this one does');
    }

    public function testBandwidthIsCountedMonthly(): void
    {
        $this->agent->succeed('system.info', ['system' => ['cpu' => ['cores' => 1]]]);
        $this->agent->succeed('system.metrics', ['cpu_percent' => 1, 'memory' => ['used_percent' => 1], 'load' => ['load1' => 0], 'disks' => [], 'network' => []]);
        $this->agent->succeed('services.status', []);
        $this->agent->succeed('discovery.certificates', []);
        $this->agent->succeed('sites.usage', [['domain' => 'shop.test', 'bytes' => 1048576, 'files' => 1]]);
        $month = gmdate('Y-m');
        $this->agent->succeed('sites.bandwidth', [['domain' => 'shop.test', 'inode' => 7, 'offset' => 900, 'months' => ['2020-01' => 999999999, $month => 8 * 1048576]]]);
        $mon = $this->app->container->get(MonitoringService::class);
        $mon->run();
        self::assertSame([8 * 1048576, 7, 900], array_map('intval', array_values((array) $this->db->fetch('SELECT bw_bytes, bw_inode, bw_offset FROM website_usage WHERE website_id = ?', [$this->siteId]))), 'other months are not counted');
        self::assertSame(['domain' => 'shop.test', 'root' => (string) $this->site()['root'], 'inode' => 0, 'offset' => 0], $this->lastCall('sites.bandwidth')['sites'][0]);

        // The next hour continues from where the log was read; over the plan: an alert.
        $this->db->run("UPDATE website_usage SET measured_at = '2000-01-01T00:00:00+00:00'");
        $this->agent->succeed('sites.bandwidth', [['domain' => 'shop.test', 'inode' => 7, 'offset' => 1900, 'months' => [$month => 3 * 1048576]]]);
        $mon->run();
        self::assertSame(['inode' => 7, 'offset' => 900], array_intersect_key($this->lastCall('sites.bandwidth')['sites'][0], ['inode' => 0, 'offset' => 0]));
        self::assertSame(11 * 1048576, (int) $this->db->scalar('SELECT bw_bytes FROM website_usage WHERE website_id = ?', [$this->siteId]));
        self::assertStringContainsString('cust used 11 MB of a 10 MB monthly bandwidth', (string) $this->db->scalar("SELECT message FROM alerts WHERE rule = 'bandwidth'"));

        // A new month starts from zero.
        $this->db->run("UPDATE website_usage SET bw_month = '2020-01', measured_at = '2000-01-01T00:00:00+00:00'");
        $this->agent->succeed('sites.bandwidth', [['domain' => 'shop.test', 'inode' => 7, 'offset' => 2000, 'months' => [$month => 1048576]]]);
        $mon->run();
        self::assertSame([1048576, $month], [(int) $this->db->scalar('SELECT bw_bytes FROM website_usage'), (string) $this->db->scalar('SELECT bw_month FROM website_usage')]);

        // Customers see their plan usage on their home page.
        $this->agent->succeed('ftp.status', ['accounts' => []]);
        $this->login('cust');
        $page = $this->request('GET', '/websites')->body();
        self::assertStringContainsString('id="plan-usage"', $page);
        self::assertMatchesRegularExpression('#data-usage="bandwidth">.*?1 / 10#s', $page);
        self::assertMatchesRegularExpression('#data-usage="parked_domains">.*?1 / 1#s', $page);
        self::assertMatchesRegularExpression('#data-usage="ftp_accounts">.*?0 / 1#s', $page);
    }
}
