<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Tests\Support\HttpTestCase;

/** The new-website page is a four-step wizard over the same form. */
final class CreateWizardTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->agent->succeed('sites.apply', ['applied' => true]);
        $this->login('admin');
    }

    public function testThePageHasFourSteps(): void
    {
        $page = $this->request('GET', '/websites/new')->body();
        self::assertStringContainsString('data-nim-wizard', $page);
        foreach ([1, 2, 3, 4] as $n) {
            self::assertStringContainsString('data-nim-step-tab="' . $n . '"', $page, 'the progress bar');
            self::assertStringContainsString('data-nim-step="' . $n . '"', $page);
        }
        self::assertStringNotContainsString('data-nim-step="5"', $page);
        foreach (['Domain and owner', 'Review and create'] as $heading) {
            self::assertStringContainsString($heading, $page, 'each step has its own heading');
        }
        self::assertStringContainsString('class="nim-type-card"', $page, 'step 1: the type cards');
        self::assertStringContainsString('data-nim-review', $page, 'step 4: the review');
        foreach (['data-nim-wizard-back', 'data-nim-wizard-next', 'data-nim-wizard-submit'] as $button) {
            self::assertStringContainsString($button, $page, $button);
        }
        // What belongs to which step: domain and owner are step 2, the type's settings and the folded advanced ones step 3.
        $at = static fn (string $needle): int => (int) strpos($page, $needle);
        self::assertGreaterThan($at('data-nim-step="2"'), $at('name="domain"'));
        self::assertLessThan($at('<summary>Advanced settings</summary>'), $at('name="php_version"'));
        self::assertGreaterThan($at('<summary>Advanced settings</summary>'), $at('name="system_user"'), 'advanced settings are folded');
    }

    public function testTheSettingsPageIsNotAWizard(): void
    {
        $this->post('/websites/new', ['domain' => 'wiz.test', 'kind' => 'static']);
        $id = (int) $this->db()->scalar("SELECT id FROM websites WHERE domain = 'wiz.test'");
        $page = $this->request('GET', '/sites/' . $id . '/settings')->body();
        self::assertStringNotContainsString('data-nim-wizard', $page);
        self::assertStringNotContainsString('data-nim-step=', $page);
    }

    public function testCanOpenTheAppInstallerAfterwards(): void
    {
        $res = $this->post('/websites/new', ['domain' => 'blog.test', 'kind' => 'static', 'after_apps' => '1']);
        $id = (int) $this->db()->scalar("SELECT id FROM websites WHERE domain = 'blog.test'");
        self::assertStringEndsWith('/sites/' . $id . '/apps', $res->header('Location') ?? '');
    }

    public function testTheWizardOpensInAWindowWithoutTheWholePage(): void
    {
        $res = $this->request('GET', '/websites/new/modal', [], ['x-requested-with' => 'fetch']);
        self::assertSame(200, $res->status());
        $html = $res->body();
        self::assertStringNotContainsString('<html', $html, 'only the window content, not a page');
        self::assertStringContainsString('class="modal-header"', $html);
        self::assertStringContainsString('data-nim-wizard', $html);
        self::assertStringContainsString('data-nim-site-modal-form', $html, 'sent in the background');
        self::assertStringContainsString('data-nim-modal-error', $html);
        foreach ([1, 2, 3, 4] as $n) {
            self::assertStringContainsString('data-nim-step="' . $n . '"', $html);
        }
        self::assertStringContainsString('name="domain"', $html);
        self::assertStringNotContainsString('data-nim-busy', $html, 'the window sends its own form');
    }

    public function testCreateWebsiteLinksOpenTheWindowAndStillWorkAsPageLinks(): void
    {
        $page = $this->request('GET', '/')->body();
        self::assertStringContainsString('href="/websites/new" data-nim-site-modal data-nim-modal-href="/websites/new/modal"', $page);
        self::assertStringContainsString('id="nim-site-modal"', $page, 'the one shared window is on every page');
        self::assertSame(1, substr_count($page, 'id="nim-site-modal"'));
        // The websites list (it needs a discovery to draw) and the other themes use the same attributes on "Add site".
        $views = dirname(__DIR__, 2) . '/resources/views';
        foreach (['websites/index.php', 'themes/classic/pages/websites/index.php', 'themes/compact/pages/websites/index.php', 'themes/simple/pages/websites/index.php', 'themes/workspace/pages/websites/index.php'] as $file) {
            $src = (string) file_get_contents($views . '/' . $file);
            self::assertStringNotContainsString("href=\"<?= e(\$this->url('sites.create')) ?>\">", $src, $file . ' has a create link that still leaves the page');
            self::assertStringContainsString("data-nim-modal-href=\"<?= e(\$this->url('sites.create.modal')) ?>\"", $src, $file);
        }
    }

    public function testASubmitFromTheWindowIsAnsweredAsJson(): void
    {
        $json = ['accept' => 'application/json', 'x-requested-with' => 'fetch'];
        $bad = $this->request('POST', '/websites/new', ['domain' => 'not a domain', 'kind' => 'static', '_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()], $json);
        self::assertSame(422, $bad->status());
        $body = (array) json_decode($bad->body(), true);
        self::assertFalse($body['success']);
        self::assertArrayHasKey('domain', (array) $body['error']['errors'], 'the field is marked in the window');

        $ok = $this->request('POST', '/websites/new', ['domain' => 'inwin.test', 'kind' => 'static', '_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()], $json);
        self::assertSame(200, $ok->status(), $ok->body());
        $body = (array) json_decode($ok->body(), true);
        $id = (int) $this->db()->scalar("SELECT id FROM websites WHERE domain = 'inwin.test'");
        self::assertTrue($body['success']);
        self::assertSame('/sites/' . $id, $body['redirect']);
    }

    /** @param array<string, mixed> $body */
    private function wizardPost(array $body): array
    {
        $r = $this->request('POST', '/websites/new', $body + ['wizard' => '1', 'kind' => 'static', '_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()], ['accept' => 'application/json', 'x-requested-with' => 'fetch']);
        return ['status' => $r->status()] + (array) json_decode($r->body(), true);
    }

    public function testAnAdministratorMustSayWhoTheWebsiteBelongsTo(): void
    {
        $page = $this->request('GET', '/websites/new')->body();
        foreach (['name="owner_mode" value="existing" required', 'name="owner_mode" value="new" required', 'name="owner_mode" value="none" required', 'name="wizard" value="1"'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertStringNotContainsString('name="owner_mode" value="existing" required checked', $page, 'no choice is made for them');

        $r = $this->wizardPost(['domain' => 'noowner.test']);
        self::assertSame(422, $r['status']);
        self::assertArrayHasKey('owner_mode', $r['error']['errors']);
        self::assertSame(0, (int) $this->db()->scalar("SELECT COUNT(*) FROM websites WHERE domain = 'noowner.test'"), 'nothing is created without an owner choice');
    }

    public function testAnExistingCustomerOrNobodyCanOwnIt(): void
    {
        $custId = $this->seedUser('cust1', 'customer');
        $bad = $this->wizardPost(['domain' => 'pick.test', 'owner_mode' => 'existing']);
        self::assertSame(422, $bad['status']);
        self::assertArrayHasKey('owner_id', $bad['error']['errors'], 'an existing customer must be chosen');
        $admin = (int) $this->db()->scalar("SELECT id FROM users WHERE username = 'admin'");
        $notCustomer = $this->wizardPost(['domain' => 'pick.test', 'owner_mode' => 'existing', 'owner_id' => (string) $admin]);
        self::assertSame(422, $notCustomer['status'], 'an administrator is not a customer');

        $ok = $this->wizardPost(['domain' => 'owned.test', 'owner_mode' => 'existing', 'owner_id' => (string) $custId]);
        self::assertSame(200, $ok['status'], json_encode($ok));
        self::assertSame($custId, (int) $this->db()->scalar("SELECT owner_id FROM websites WHERE domain = 'owned.test'"));

        $none = $this->wizardPost(['domain' => 'admins.test', 'owner_mode' => 'none', 'owner_id' => (string) $custId]);
        self::assertSame(200, $none['status'], json_encode($none));
        self::assertNull($this->db()->scalar("SELECT owner_id FROM websites WHERE domain = 'admins.test'"), 'no customer means no owner, whatever else was sent');
    }

    public function testANewCustomerAccountIsCreatedWithTheWebsite(): void
    {
        $r = $this->wizardPost(['domain' => 'fresh-shop.test', 'owner_mode' => 'new', 'new_email' => 'owner@fresh-shop.test']);
        self::assertSame(200, $r['status'], json_encode($r));
        $uid = (int) $this->db()->scalar("SELECT id FROM users WHERE username = 'fresh-shop'");
        self::assertGreaterThan(0, $uid, 'the login is named after the domain');
        self::assertSame($uid, (int) $this->db()->scalar("SELECT owner_id FROM websites WHERE domain = 'fresh-shop.test'"));
        self::assertSame('customer', (string) $this->db()->scalar('SELECT r.name FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = ?', [$uid]));
        $secret = (array) $this->flash('secret');
        self::assertSame('fresh-shop', $secret['user'] ?? null);
        self::assertGreaterThanOrEqual(16, strlen((string) ($secret['password'] ?? '')), 'a generated password is shown once');
    }

    public function testARefusedNewCustomerLeavesNothingBehind(): void
    {
        $r = $this->wizardPost(['domain' => 'nope.test', 'owner_mode' => 'new', 'new_email' => 'not-an-email']);
        self::assertSame(422, $r['status']);
        self::assertArrayHasKey('new_email', $r['error']['errors']);
        $r = $this->wizardPost(['domain' => 'nope.test', 'owner_mode' => 'new', 'new_email' => 'a@nope.test', 'new_username' => 'Bad Name!']);
        self::assertArrayHasKey('new_username', $r['error']['errors']);
        self::assertSame(0, (int) $this->db()->scalar("SELECT COUNT(*) FROM users WHERE email = 'a@nope.test'"));
        self::assertSame(0, (int) $this->db()->scalar("SELECT COUNT(*) FROM websites WHERE domain = 'nope.test'"));
        // A taken login name is refused too, and the customer that was about to be made is not kept.
        $this->seedUser('taken', 'customer');
        $r = $this->wizardPost(['domain' => 'tk.test', 'owner_mode' => 'new', 'new_email' => 'b@tk.test', 'new_username' => 'taken']);
        self::assertSame(422, $r['status']);
        self::assertArrayHasKey('new_username', $r['error']['errors']);
    }

    public function testAHostingCustomersWebsiteBelongsToThemWithoutAskingWho(): void
    {
        $cust = $this->seedUser('mine', 'customer');
        $this->post('/logout');
        $this->login('mine');
        $page = $this->request('GET', '/websites/new')->body();
        self::assertStringContainsString('Website owner', $page);
        self::assertStringContainsString('(you)', $page, 'the signed-in user is the owner, shown and not asked');
        self::assertStringNotContainsString('name="owner_mode"', $page);
        self::assertStringNotContainsString('name="wizard"', $page);
        $r = $this->request('POST', '/websites/new', ['domain' => 'mine.test', 'kind' => 'static', 'owner_id' => '1', 'owner_mode' => 'none', 'wizard' => '1', '_token' => $this->app->container->get(\NimVor\Security\Csrf::class)->token()], ['accept' => 'application/json', 'x-requested-with' => 'fetch']);
        self::assertSame(200, $r->status(), $r->body());
        self::assertSame($cust, (int) $this->db()->scalar("SELECT owner_id FROM websites WHERE domain = 'mine.test'"), 'whatever the form said, a customer owns what they create');
    }

    private function db(): \NimVor\Database\Connection
    {
        return $this->app->container->get(\NimVor\Database\Connection::class);
    }
}
