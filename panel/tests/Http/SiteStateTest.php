<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\ArraySession;
use NimVor\Services\ApiTokenService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/**
 * Enabling, disabling and deleting a website: customers cannot switch their
 * website, are told when an administrator disabled it, and cannot delete it
 * while it is disabled (pages and endpoints alike).
 */
final class SiteStateTest extends HttpTestCase
{
    private int $siteId;
    private int $otherSite;
    private Connection $db;
    /** @var array<string, mixed>|int */
    private $adminUser;
    /** @var array<string, mixed>|int */
    private $custUser;

    protected function setUp(): void
    {
        parent::setUp();
        $this->adminUser = $this->seedUser('admin', 'admin');
        $this->custUser = $this->seedUser('cust', 'customer');
        $cust = $this->custUser;
        $this->seedUser('res', 'reseller');
        $other = $this->seedUser('other', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        foreach ([['shop.test', $cust], ['blog.test', $other]] as [$d, $o]) {
            $this->db->run("INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, status, enabled, created_at, updated_at) VALUES (1, ?, 'static', ?, ?, ?, ?, 'active', 1, 'x', 'x')", [$d, '/home/u/' . $d, '/home/u/' . $d . '/public_html', 'u' . $o, $o]);
        }
        $this->siteId = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'shop.test'");
        $this->otherSite = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'blog.test'");
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->agent->succeed('sites.delete', ['removed' => true]);
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    private function enabled(int $id): int
    {
        return (int) $this->db->scalar('SELECT enabled FROM websites WHERE id = ?', [$id]);
    }

    private function exists(int $id): bool
    {
        return (int) $this->db->scalar('SELECT COUNT(*) FROM websites WHERE id = ?', [$id]) === 1;
    }

    private function api(string $method, string $path, string $token): Response
    {
        return $this->app->handle(new Request($method, $path, [], [], ['REMOTE_ADDR' => '192.0.2.70'], ['authorization' => 'Bearer ' . $token, 'content-type' => 'application/json'], ''));
    }

    public function testDerivedAccountNameAvoidsAnotherAccountsName(): void
    {
        $svc = $this->app->container->get(WebsiteService::class);
        // "u<otherId>" is taken by another customer; a derived name that equals it must not be reused.
        $taken = 'blogacct';
        $this->db->run('UPDATE websites SET system_user = ? WHERE id = ?', [$taken, $this->otherSite]);
        $domain = $taken . '.example.net';
        $row = $svc->validate(['domain' => $domain, 'kind' => 'static'], null, false)['row'];
        self::assertSame($taken . '2', $row['system_user']);
    }

    public function testDerivedAccountNameNeverUsesAReservedName(): void
    {
        $svc = $this->app->container->get(WebsiteService::class);
        $row = $svc->validate(['domain' => 'nimvor.com', 'kind' => 'static'], null, false)['row'];
        self::assertSame('nimvor2', $row['system_user']);
    }

    public function testCustomersDoNotSeeTheEnableDisableOption(): void
    {
        $this->switchTo('cust');
        $overview = $this->request('GET', '/sites/' . $this->siteId)->body();
        self::assertStringNotContainsString('/sites/' . $this->siteId . '/toggle', $overview);
        self::assertStringNotContainsString('Disable the website', $overview);
        self::assertStringContainsString('Delete the website', $overview, 'an enabled website can still be deleted');
        self::assertStringNotContainsString('id="site-disabled"', $overview);
        $advanced = $this->request('GET', '/sites/' . $this->siteId . '/advanced')->body();
        self::assertStringNotContainsString('/sites/' . $this->siteId . '/toggle', $advanced);
        self::assertStringNotContainsString('Disable website', $advanced);
        self::assertStringContainsString('id="delete-website"', $advanced);
        $list = $this->request('GET', '/websites')->body();
        self::assertStringNotContainsString('/sites/' . $this->siteId . '/toggle', $list, 'no switch in the list either');
        self::assertStringContainsString('shop.test', $list);
    }

    public function testCustomersCannotToggleEvenByPostingTheEndpoint(): void
    {
        $this->switchTo('cust');
        $this->post('/sites/' . $this->siteId . '/toggle', ['return_to' => 'overview']);
        self::assertStringContainsString('Only an administrator', (string) $this->flash('error'));
        self::assertSame(1, $this->enabled($this->siteId));
        self::assertSame(0, $this->calls('sites.apply'));
    }

    public function testAdminsAndResellersStillSwitchWebsites(): void
    {
        $this->login('admin');
        $overview = $this->request('GET', '/sites/' . $this->siteId)->body();
        self::assertStringContainsString('Disable the website', $overview);
        self::assertStringNotContainsString('id="site-disabled"', $overview, 'the banner is for the owner, not for the administrator');
        $this->post('/sites/' . $this->siteId . '/toggle', ['return_to' => 'overview']);
        self::assertSame(0, $this->enabled($this->siteId));
        self::assertSame(1, $this->calls('sites.apply'));
        $this->post('/sites/' . $this->siteId . '/toggle', ['return_to' => 'overview']);
        self::assertSame(1, $this->enabled($this->siteId));

        $this->db->run('UPDATE users SET parent_id = NULL');
        $reseller = (int) $this->db->scalar("SELECT id FROM users WHERE username = 'res'");
        $this->db->run('UPDATE users SET parent_id = ? WHERE username = ?', [$reseller, 'cust']);
        $this->switchTo('res');
        $this->post('/sites/' . $this->siteId . '/toggle', ['return_to' => 'overview']);
        self::assertSame(0, $this->enabled($this->siteId), "a reseller manages their customers' websites");
    }

    public function testCustomersSeeAMessageWhenTheAdministratorDisabledTheirWebsite(): void
    {
        $this->login('admin');
        $this->post('/sites/' . $this->siteId . '/toggle', ['return_to' => 'overview']);
        self::assertSame(0, $this->enabled($this->siteId));

        $this->switchTo('cust');
        $overview = $this->request('GET', '/sites/' . $this->siteId)->body();
        self::assertStringContainsString('id="site-disabled"', $overview);
        self::assertStringContainsString('disabled by the administrator', $overview);
        // The message comes before the rest of the page.
        self::assertLessThan(strpos($overview, 'Site managing'), strpos($overview, 'id="site-disabled"'));
        self::assertStringNotContainsString('Delete the website', $overview);
        self::assertStringNotContainsString('Enable the website', $overview);
        // The other customer's website is unaffected and unreadable.
        self::assertSame(404, $this->request('GET', '/sites/' . $this->otherSite)->status());
    }

    public function testDeletionIsHiddenAndRefusedForCustomersWhileDisabled(): void
    {
        $this->login('admin');
        $this->post('/sites/' . $this->siteId . '/toggle', ['return_to' => 'overview']);
        $this->switchTo('cust');

        $advanced = $this->request('GET', '/sites/' . $this->siteId . '/advanced')->body();
        self::assertStringNotContainsString('/sites/' . $this->siteId . '/delete', $advanced);
        self::assertStringNotContainsString('name="confirm_domain"', $advanced);
        self::assertStringContainsString('cannot be deleted from here', $advanced);

        $this->post('/sites/' . $this->siteId . '/delete', ['confirm_domain' => 'shop.test', 'remove_files' => '1', 'remove_user' => '1']);
        self::assertStringContainsString('disabled by the administrator', (string) $this->flash('error'));
        self::assertTrue($this->exists($this->siteId), 'the website is still there');
        self::assertSame(0, $this->calls('sites.delete'), 'the agent was never asked');
    }

    public function testThePolicyTreatsSuspendedAccountsLikeDisabledWebsites(): void
    {
        $customer = ['role_scope' => 'own', 'role_name' => 'customer'];
        $reseller = ['role_scope' => 'own', 'role_name' => 'reseller'];
        $admin = ['role_scope' => 'all', 'role_name' => 'admin'];
        $site = ['enabled' => true, 'account_suspended' => false];
        self::assertTrue(WebsiteService::mayDelete($customer, $site));
        self::assertFalse(WebsiteService::mayDelete($customer, ['enabled' => false] + $site));
        self::assertFalse(WebsiteService::mayDelete($customer, ['account_suspended' => true] + $site));
        self::assertTrue(WebsiteService::mayDelete($admin, ['enabled' => false] + $site));
        self::assertTrue(WebsiteService::mayDelete($reseller, ['enabled' => false] + $site));
        self::assertFalse(WebsiteService::mayToggle($customer));
        self::assertTrue(WebsiteService::mayToggle($admin));
        self::assertTrue(WebsiteService::mayToggle($reseller));
        // A custom role with its own scope is treated like a customer.
        self::assertFalse(WebsiteService::mayToggle(['role_scope' => 'own', 'role_name' => 'staff']));
    }

    public function testEnabledWebsitesCanStillBeDeletedByTheirCustomer(): void
    {
        $this->switchTo('cust');
        $this->post('/sites/' . $this->siteId . '/delete', ['confirm_domain' => 'wrong.test']);
        self::assertTrue($this->exists($this->siteId), 'the typed confirmation still applies');
        $this->post('/sites/' . $this->siteId . '/delete', ['confirm_domain' => 'shop.test']);
        self::assertFalse($this->exists($this->siteId));
        self::assertSame(1, $this->calls('sites.delete'));
        // And never another customer's website.
        $this->post('/sites/' . $this->otherSite . '/delete', ['confirm_domain' => 'blog.test']);
        self::assertTrue($this->exists($this->otherSite));
    }

    public function testAdministratorsCanDeleteADisabledWebsite(): void
    {
        $this->login('admin');
        $this->post('/sites/' . $this->siteId . '/toggle', ['return_to' => 'overview']);
        $this->post('/sites/' . $this->siteId . '/delete', ['confirm_domain' => 'shop.test']);
        self::assertFalse($this->exists($this->siteId));
        self::assertSame(1, $this->calls('sites.delete'));
    }

    public function testTheApiFollowsTheSameRules(): void
    {
        $tokens = $this->app->container->get(ApiTokenService::class);
        $custToken = $tokens->create($this->custUser, ['name' => 'rw', 'ability' => 'write', 'expires_days' => 1]);
        foreach (['enable', 'disable'] as $verb) {
            $res = $this->api('POST', '/api/v1/websites/' . $this->siteId . '/' . $verb, $custToken);
            self::assertContains($res->status(), [403, 409, 422, 400], 'customer ' . $verb . ': ' . $res->body());
            self::assertSame(1, $this->enabled($this->siteId));
        }
        self::assertSame(0, $this->calls('sites.apply'));

        // Disabled by the administrator: the customer cannot delete it through the API.
        $adminToken = $tokens->create($this->adminUser, ['name' => 'rw', 'ability' => 'write', 'expires_days' => 1]);
        self::assertSame(200, $this->api('POST', '/api/v1/websites/' . $this->siteId . '/disable', $adminToken)->status());
        self::assertSame(0, $this->enabled($this->siteId));
        $res = $this->api('DELETE', '/api/v1/websites/' . $this->siteId, $custToken);
        self::assertContains($res->status(), [403, 409, 422, 400], $res->body());
        self::assertTrue($this->exists($this->siteId));
        self::assertSame(0, $this->calls('sites.delete'));
        // The administrator can.
        self::assertSame(200, $this->api('DELETE', '/api/v1/websites/' . $this->siteId, $adminToken)->status());
        self::assertFalse($this->exists($this->siteId));
    }
}
