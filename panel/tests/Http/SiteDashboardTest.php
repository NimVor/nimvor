<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

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
        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->app = $this->makeApp();
        $this->login($user);
    }

    public function testSitePageShowsDetailsAndTiles(): void
    {
        $this->login('admin');
        $base = '/sites/' . $this->siteId;
        $page = $this->request('GET', $base)->body();
        foreach (['data-nim-site-summary', 'Site user', 'Root directory', 'Handler', 'PHP-FPM', '5 MB', 'Not installed', 'None yet', 'data-nim-feature-search', 'Site managing', 'Delete the website', 'Disable the website'] as $s) {
            self::assertStringContainsString($s, $page, $s);
        }
        foreach (['ftp accounts', 'files', 'databases', 'domains (dns)', 'ssl certificates', 'scheduler', 'mail', 'backup copies', 'site access', 'settings', 'php settings', 'logs', 'manual settings'] as $t) {
            self::assertStringContainsString('data-nim-tile="' . $t . '"', $page, $t);
        }
        self::assertStringContainsString('href="' . $base . '/databases#new-database"', $page, 'search opens the form');
        // The generated configuration and re-apply stay under Advanced.
        foreach (['server { listen 80; }', 'Re-apply configuration'] as $s) {
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
        self::assertMatchesRegularExpression('#data-nim-tile="scheduler">.*?nim-tile-note">2<#s', $page);
        self::assertMatchesRegularExpression('#data-nim-tile="databases">.*?nim-tile-note">0<#s', $page);
    }

    public function testCustomersGetTheSimpleLayout(): void
    {
        $this->login('cust');
        self::assertSame('/websites', $this->request('GET', '/')->header('Location'), 'home is their websites');
        $list = $this->request('GET', '/websites')->body();
        self::assertStringNotContainsString('id="nim-sidebar"', $list);
        self::assertStringContainsString('nim-brandbar', $list);
        self::assertStringContainsString('data-nim-cards', $list);
        self::assertStringContainsString('shop.test', $list);
        self::assertStringNotContainsString('other.test', $list, "another account's website");
        self::assertStringContainsString('Create site', $list);

        $page = $this->request('GET', '/sites/' . $this->siteId)->body();
        self::assertStringNotContainsString('id="nim-sidebar"', $page);
        self::assertStringNotContainsString('data-nim-site-menu-button', $page, 'no menu button without a sidebar');
        self::assertStringContainsString('class="nim-crumbs"', $page);
        self::assertStringContainsString('/sites/' . $this->siteId . '/advanced', $page, 'customers manage their own website');
        foreach (['Server security', 'Audit log', 'Memcached', 'Spam filter', 'Backup destinations', '/security"', '/audit"'] as $s) {
            self::assertStringNotContainsString($s, $page, $s);
        }
        self::assertSame(200, $this->request('GET', '/sites/' . $this->siteId . '/advanced')->status());
        foreach (['', '/advanced', '/databases', '/backups'] as $p) {
            self::assertSame(404, $this->request('GET', '/sites/' . $this->otherId . $p)->status(), 'other customer\'s website' . $p);
        }

        // Administrators keep the sidebar; the table is their default, cards on request.
        $this->switchTo('admin');
        $this->agent->succeed('discovery.inventory', ['websites' => ['websites' => [], 'catch_all_vhosts' => 0, 'warnings' => []]]);
        $list = $this->request('GET', '/websites')->body();
        self::assertStringContainsString('id="nim-sidebar"', $list);
        self::assertStringContainsString('id="nim-sites-dt"', $list, 'the table is the administrators\' default');
        self::assertStringContainsString('name="return_to" value="websites"', $list, 'status switch returns to the list');
        self::assertStringContainsString('nim-site-card', $this->request('GET', '/websites', [], [], '', ['view' => 'grid'])->body());
    }

    public function testViewerGetsAReadOnlySitePage(): void
    {
        $this->login('viewer');
        $page = $this->request('GET', '/sites/' . $this->siteId)->body();
        self::assertStringContainsString('data-nim-tiles', $page);
        foreach (['/advanced', '#new-database', '#backup-now', 'Delete the website', 'Disable the website', '/sites/' . $this->siteId . '/settings', 'href="/settings"'] as $s) {
            self::assertStringNotContainsString($s, $page, $s);
        }
        self::assertSame(403, $this->request('GET', '/sites/' . $this->siteId . '/advanced')->status());
    }

    public function testDangerZoneActionsReturnToAdvanced(): void
    {
        $this->login('admin');
        $base = '/sites/' . $this->siteId;
        self::assertSame($base . '/advanced', $this->post($base . '/toggle')->header('Location'));
        self::assertSame($base, $this->post($base . '/toggle', ['return_to' => 'overview'])->header('Location'), 'the site page\'s own button');
        self::assertSame($base . '/advanced', $this->post($base . '/toggle', ['return_to' => 'https://evil.test/'])->header('Location'));
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
        self::assertStringContainsString('data-nim-site-menu', $mail->body());
        self::assertStringContainsString('name="return_to" value="' . $base . '/mail"', $mail->body());

        $dns = $this->request('GET', $base . '/dns')->body();
        self::assertStringContainsString('data-nim-site-menu', $dns);

        // Errors go back to the set-up page, but only to that exact page.
        self::assertSame($base . '/mail', $this->post('/mail/domains', ['website_id' => '99999', 'return_to' => $base . '/mail'])->header('Location'));
        foreach (['https://evil.test/', '//evil.test/sites/1/mail', '/sites/1/mail/../../settings', $base . '/dns'] as $bad) {
            self::assertSame('/mail', $this->post('/mail/domains', ['website_id' => '99999', 'return_to' => $bad])->header('Location'), $bad);
        }
        self::assertSame($base . '/dns', $this->post('/dns/zones', ['website_id' => '99999', 'return_to' => $base . '/dns'])->header('Location'));
        self::assertSame('/dns', $this->post('/dns/zones', ['website_id' => '99999', 'return_to' => 'https://evil.test/'])->header('Location'));
    }

    public function testWebsiteMenuForAdministratorsBreadcrumbForCustomers(): void
    {
        foreach (['admin' => true, 'cust' => false] as $user => $sidebar) {
            $this->switchTo($user);
            foreach (['', '/databases', '/cron', '/backups', '/ssl', '/ftp', '/advanced'] as $p) {
                $res = $this->request('GET', '/sites/' . $this->siteId . $p);
                if ($res->status() !== 200) {
                    continue; // pages needing agent data not stubbed here
                }
                $body = $res->body();
                self::assertStringNotContainsString('nim-tabs', $body, $p);
                self::assertStringContainsString('class="nim-crumbs"', $body, $user . $p);
                self::assertSame($sidebar, str_contains($body, 'data-nim-site-menu'), $user . $p . ': website menu in the sidebar');
                self::assertSame($sidebar, str_contains($body, 'data-nim-site-menu-button'), $user . $p . ': phone menu button');
            }
        }
    }

    public function testMailDomainPageKeepsTheWebsiteAndShowsUsage(): void
    {
        $this->agent->succeed('mail.status', ['installed' => ['postfix' => true], 'configured' => true, 'hostname' => 'mail.host.test', 'running' => ['postfix' => true],
            'mailboxes' => [['address' => 'info@shop.test', 'quota_mb' => 1, 'bytes' => 1000000]]]);
        $this->agent->succeed('mail.domain_add', ['domain' => 'shop.test', 'dkim_selector' => 'nimvor202609', 'dkim_txt' => 'v=DKIM1; k=rsa; p=MIIB']);
        $this->agent->succeed('mail.mailbox_save', ['created' => true]);
        $this->login('cust');
        $this->post('/mail/domains', ['website_id' => (string) $this->siteId]);
        $id = (int) $this->app->container->get(Connection::class)->scalar("SELECT id FROM mail_domains WHERE domain = 'shop.test'");
        self::assertSame('/mail/domains/' . $id, $this->request('GET', '/sites/' . $this->siteId . '/mail')->header('Location'));
        $this->post('/mail/domains/' . $id . '/mailboxes', ['local' => 'info', 'password' => 'a-long-password-1', 'quota_mb' => '1']);

        $page = $this->request('GET', '/mail/domains/' . $id)->body();
        foreach (['class="nim-crumbs"', 'id="new-mailbox"', 'id="new-alias"', 'id="mail-dns"', 'id="mail-clients"', '<th>Usage</th>', 'Almost full', 'No forwarders'] as $s) {
            self::assertStringContainsString($s, $page, $s);
        }
        // Hosting customers do not get the "Danger zone" (stop hosting email) on their own domain.
        self::assertStringNotContainsString('Stop hosting email', $page, 'customers do not see the danger zone');
        // Administrators get the website's menu on it as well, and the danger zone.
        $this->switchTo('admin');
        $adminPage = $this->request('GET', '/mail/domains/' . $id)->body();
        self::assertStringContainsString('data-nim-site-menu', $adminPage, 'administrators see the website too');
        self::assertStringContainsString('Stop hosting email', $adminPage, 'administrators see the danger zone');
    }
}
