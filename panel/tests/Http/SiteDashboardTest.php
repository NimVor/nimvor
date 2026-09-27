<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

/**
 * The website dashboard, its menu, Advanced, and the set-up pages for
 * email and DNS: what each role sees, and that server details moved off
 * the first screen.
 */
final class SiteDashboardTest extends HttpTestCase
{
    private int $siteId;
    private int $otherId;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $this->seedUser('viewer', 'viewer');
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->siteId = $sites->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3'], (array) $users->find($cust));
        $this->otherId = $sites->create(['domain' => 'other.test', 'kind' => 'static'], (array) $users->find($admin));
        $this->agent->succeed('sites.inspect', ['config_exists' => true, 'disk_bytes' => 5 * 1048576, 'disk_complete' => true, 'config_preview' => 'server { listen 80; }']);
    }

    private function switchTo(string $user): void
    {
        $this->session = new \WhaCP\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login($user);
    }

    public function testDashboardIsFriendlyAndServerDetailsAreUnderAdvanced(): void
    {
        $this->login('admin');
        $base = '/sites/' . $this->siteId;
        $page = $this->request('GET', $base)->body();
        foreach (['data-wcp-site-summary', 'data-wcp-feature-search', 'Quick actions', 'data-wcp-site-menu', '5 MB', 'Not installed', 'None yet'] as $s) {
            self::assertStringContainsString($s, $page, $s);
        }
        foreach (['Files', 'Databases', 'Email', 'Domains', 'Applications', 'Performance', 'Backups', 'Automation', 'Security'] as $g) {
            self::assertStringContainsString('data-wcp-feature-group="' . strtolower($g) . '"', $page, $g);
        }
        self::assertStringContainsString('href="' . $base . '/databases#new-database"', $page, 'quick action opens the form');
        self::assertStringContainsString('href="' . $base . '/backups#backup-now"', $page);
        // Server details are not on the first screen.
        foreach (['System user', '/home/', 'server { listen 80; }', 'Delete website', 'Re-apply'] as $s) {
            self::assertStringNotContainsString($s, $page, $s);
        }

        $adv = $this->request('GET', $base . '/advanced')->body();
        foreach (['System user', 'Document root', 'server { listen 80; }', 'Danger Zone', 'Re-apply configuration', 'name="confirm_domain"'] as $s) {
            self::assertStringContainsString($s, $adv, $s);
        }
    }

    public function testCountsComeFromThePanelsRecords(): void
    {
        $this->login('admin');
        $db = $this->app->container->get(Connection::class);
        foreach (['php x', 'php y'] as $cmd) {
            $db->run("INSERT INTO cron_jobs (website_id, schedule, command, created_at, updated_at) VALUES (?, '*/5 * * * *', ?, ?, ?)", [$this->siteId, $cmd, gmdate('c'), gmdate('c')]);
        }
        $page = $this->request('GET', '/sites/' . $this->siteId)->body();
        self::assertStringContainsString('2 jobs', $page);
        self::assertStringContainsString('0 databases', $page);
        self::assertStringContainsString('0 workers', $page);
    }

    public function testCustomerSeesOnlyTheirFeaturesAndWebsites(): void
    {
        $this->login('cust');
        $page = $this->request('GET', '/sites/' . $this->siteId)->body();
        self::assertStringContainsString('Your sign-in security', $page);
        self::assertStringContainsString('/sites/' . $this->siteId . '/advanced', $page, 'customers manage their own website');
        foreach (['Server security', 'Audit log', 'Memcached', 'Spam filter', 'Backup destinations', '/security"', '/audit"'] as $s) {
            self::assertStringNotContainsString($s, $page, $s);
        }
        self::assertSame(200, $this->request('GET', '/sites/' . $this->siteId . '/advanced')->status());
        foreach (['', '/advanced', '/databases', '/backups'] as $p) {
            self::assertSame(404, $this->request('GET', '/sites/' . $this->otherId . $p)->status(), 'other customer\'s website' . $p);
        }

        $this->switchTo('admin');
        $page = $this->request('GET', '/sites/' . $this->siteId)->body();
        foreach (['Server security', 'Audit log', 'Spam filter', 'Backup destinations'] as $s) {
            self::assertStringContainsString($s, $page, $s);
        }
    }

    public function testViewerGetsAReadOnlyDashboard(): void
    {
        $this->login('viewer');
        $page = $this->request('GET', '/sites/' . $this->siteId)->body();
        self::assertStringContainsString('data-wcp-feature-groups', $page);
        foreach (['/advanced', '#new-database', '#backup-now', 'Create database', '/settings'] as $s) {
            self::assertStringNotContainsString($s, $page, $s);
        }
        self::assertSame(403, $this->request('GET', '/sites/' . $this->siteId . '/advanced')->status());
    }

    public function testDangerZoneActionsReturnToAdvanced(): void
    {
        $this->login('admin');
        $base = '/sites/' . $this->siteId;
        self::assertSame($base . '/advanced', $this->post($base . '/toggle')->header('Location'));
        self::assertSame($base . '/advanced', $this->post($base . '/delete', ['confirm_domain' => 'wrong.test'])->header('Location'));
        self::assertSame(1, (int) $this->app->container->get(Connection::class)->scalar('SELECT COUNT(*) FROM websites WHERE id = ?', [$this->siteId]));
    }

    public function testEmailAndDnsSetUpInsideTheWebsite(): void
    {
        $this->agent->succeed('mail.status', ['installed' => ['postfix' => true], 'configured' => true, 'hostname' => 'mail.host.test', 'running' => ['postfix' => true], 'mailboxes' => []]);
        $this->agent->succeed('dns.status', ['installed' => true, 'configured' => true, 'running' => true, 'zones' => []]);
        $this->login('admin');
        $base = '/sites/' . $this->siteId;

        $mail = $this->request('GET', $base . '/mail');
        self::assertSame(200, $mail->status());
        self::assertStringContainsString('Set up email', $mail->body());
        self::assertStringContainsString('data-wcp-site-menu', $mail->body());
        self::assertStringContainsString('name="return_to" value="' . $base . '/mail"', $mail->body());

        $dns = $this->request('GET', $base . '/dns')->body();
        self::assertStringContainsString('data-wcp-site-menu', $dns);

        // Errors go back to the set-up page, but only to that exact page.
        self::assertSame($base . '/mail', $this->post('/mail/domains', ['website_id' => '99999', 'return_to' => $base . '/mail'])->header('Location'));
        foreach (['https://evil.test/', '//evil.test/sites/1/mail', '/sites/1/mail/../../settings', $base . '/dns'] as $bad) {
            self::assertSame('/mail', $this->post('/mail/domains', ['website_id' => '99999', 'return_to' => $bad])->header('Location'), $bad);
        }
        self::assertSame($base . '/dns', $this->post('/dns/zones', ['website_id' => '99999', 'return_to' => $base . '/dns'])->header('Location'));
        self::assertSame('/dns', $this->post('/dns/zones', ['website_id' => '99999', 'return_to' => 'https://evil.test/'])->header('Location'));
    }

    public function testWebsiteMenuReplacesTheTabBar(): void
    {
        $this->login('cust');
        foreach (['', '/databases', '/cron', '/backups', '/ssl', '/ftp', '/advanced'] as $p) {
            $res = $this->request('GET', '/sites/' . $this->siteId . $p);
            if ($res->status() !== 200) {
                continue; // pages needing agent data not stubbed here
            }
            $body = $res->body();
            self::assertStringNotContainsString('wcp-tabs', $body, $p);
            self::assertStringContainsString('data-wcp-site-menu', $body, $p);
            self::assertStringContainsString('data-wcp-site-menu-button', $body, $p . ': phone menu button');
        }
    }

    public function testMailDomainPageKeepsTheWebsiteAndShowsUsage(): void
    {
        $this->agent->succeed('mail.status', ['installed' => ['postfix' => true], 'configured' => true, 'hostname' => 'mail.host.test', 'running' => ['postfix' => true],
            'mailboxes' => [['address' => 'info@shop.test', 'quota_mb' => 1, 'bytes' => 1000000]]]);
        $this->agent->succeed('mail.domain_add', ['domain' => 'shop.test', 'dkim_selector' => 'whacp202609', 'dkim_txt' => 'v=DKIM1; k=rsa; p=MIIB']);
        $this->agent->succeed('mail.mailbox_save', ['created' => true]);
        $this->login('cust');
        $this->post('/mail/domains', ['website_id' => (string) $this->siteId]);
        $id = (int) $this->app->container->get(Connection::class)->scalar("SELECT id FROM mail_domains WHERE domain = 'shop.test'");
        self::assertSame('/mail/domains/' . $id, $this->request('GET', '/sites/' . $this->siteId . '/mail')->header('Location'));
        $this->post('/mail/domains/' . $id . '/mailboxes', ['local' => 'info', 'password' => 'a-long-password-1', 'quota_mb' => '1']);

        $page = $this->request('GET', '/mail/domains/' . $id)->body();
        foreach (['data-wcp-site-menu', 'id="new-mailbox"', 'id="new-alias"', 'id="mail-dns"', 'id="mail-clients"', '<th>Usage</th>', 'Almost full', 'Stop hosting email', 'No forwarders'] as $s) {
            self::assertStringContainsString($s, $page, $s);
        }
        // Administrators get the website's menu on it as well.
        $this->switchTo('admin');
        self::assertStringContainsString('data-wcp-site-menu', $this->request('GET', '/mail/domains/' . $id)->body(), 'administrators see the website too');
    }
}
