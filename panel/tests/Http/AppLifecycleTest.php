<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Repositories\UserRepository;
use NimVor\Services\AppCatalogService;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** App installer lifecycle: catalog status, dry-run plan, one install per folder, outcome recording. */
final class AppLifecycleTest extends HttpTestCase
{
    private int $siteId;
    private int $otherSite;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $other = $this->seedUser('other', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $users = $this->app->container->get(UserRepository::class);
        $sites = $this->app->container->get(WebsiteService::class);
        $this->login('admin');
        $this->siteId = $sites->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3', 'owner_id' => (string) $cust], (array) $users->find($admin));
        $this->otherSite = $sites->create(['domain' => 'blog.test', 'kind' => 'php', 'php_version' => '8.3', 'owner_id' => (string) $other], (array) $users->find($admin));
        foreach (['db.create', 'db.user_create', 'db.grant', 'db.drop', 'db.user_drop'] as $a) {
            $this->agent->succeed($a, ['ok' => true]);
        }
        $this->agent->succeed('apps.install', ['job_id' => 'aaaaaaaaaaaaaaaa']);
    }

    private function switchTo(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    private function setStatus(string $slug, string $status): void
    {
        $this->db->run('UPDATE app_catalog SET status = ? WHERE slug = ?', [$status, $slug]);
    }

    private function apps(int $id): string
    {
        return '/sites/' . $id . '/apps';
    }

    public function testExistingApplicationsStayEnabled(): void
    {
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM app_catalog WHERE status <> 'enabled'"), 'the migration keeps every existing application enabled');
        $this->switchTo('cust');
        $page = $this->request('GET', $this->apps($this->siteId))->body();
        self::assertStringContainsString('Joomla', $page);
        self::assertStringContainsString('Review what it will do', $page);
        self::assertStringNotContainsString('>Testing<', $page);
    }

    public function testTestingApplicationsAreForAdministratorsOnly(): void
    {
        $this->setStatus('bludit', 'testing');
        // Administrator: sees it marked, may install it, and the agent is told so.
        $page = $this->request('GET', $this->apps($this->siteId))->body();
        self::assertStringContainsString('data-bs-target="#app-bludit"', $page);
        self::assertStringContainsString('>Testing<', $page);
        $this->post($this->apps($this->siteId) . '/bludit', []);
        self::assertTrue($this->lastCall('apps.install')['allow_testing']);
        $this->db->run("UPDATE app_installs SET status = 'succeeded'");

        // Customer: does not see it, and cannot install it by posting to the route.
        $this->switchTo('cust');
        self::assertStringNotContainsString('data-bs-target="#app-bludit"', $this->request('GET', $this->apps($this->siteId))->body(), 'no install card for a customer');
        $calls = $this->calls('apps.install');
        $this->post($this->apps($this->siteId) . '/bludit', []);
        self::assertStringContainsString('not available', (string) $this->flash('error'));
        self::assertSame($calls, $this->calls('apps.install'));
    }

    public function testCustomersNeverSendTheTestingAllowance(): void
    {
        $this->switchTo('cust');
        $this->post($this->apps($this->siteId) . '/bludit', ['allow_testing' => '1']);
        self::assertFalse($this->lastCall('apps.install')['allow_testing'], 'a customer cannot grant themselves the testing allowance');
    }

    public function testDraftDeprecatedAndDisabledCannotBeInstalledByAnyone(): void
    {
        foreach (['draft', 'deprecated', 'disabled'] as $st) {
            $this->setStatus('bludit', $st);
            $before = $this->calls('apps.install');
            $this->post($this->apps($this->siteId) . '/bludit', []);
            self::assertSame($before, $this->calls('apps.install'), $st . ' must not reach the agent');
            self::assertStringNotContainsString('data-bs-target="#app-bludit"', $this->request('GET', $this->apps($this->siteId))->body(), $st);
        }
    }

    public function testAnApplicationCannotBeOfferedToCustomersBeforeItIsProven(): void
    {
        $this->setStatus('bludit', 'testing');
        $this->post('/apps-manager/app', ['slug' => 'bludit', 'field' => 'customer_visible', 'on' => '1']);
        self::assertStringContainsString('cannot be offered to customers', (string) $this->flash('error'));
        $page = $this->request('GET', '/apps-manager')->body();
        self::assertStringContainsString('Testing', $page, 'the manager shows the status');
        // Turning it off is always allowed.
        $this->post('/apps-manager/app', ['slug' => 'bludit', 'field' => 'customer_visible', 'on' => '0']);
        self::assertSame(0, (int) $this->db->scalar("SELECT customer_visible FROM app_catalog WHERE slug = 'bludit'"));
    }

    public function testThePlanPageShowsTheAgentsDryRun(): void
    {
        $this->agent->succeed('apps.plan', [
            'app' => 'joomla', 'name' => 'Joomla', 'status' => 'enabled', 'ok' => false, 'license' => 'GPL-2.0-or-later', 'disk_needed_mb' => 300, 'disk_free_mb' => 120,
            'checks' => [['name' => 'Folder is empty', 'ok' => true], ['name' => 'Enough free disk space', 'ok' => false, 'detail' => '300 MB needed, 120 MB free']],
            'steps' => [['action' => 'Create database', 'detail' => 'a new MySQL/MariaDB database'], ['action' => 'Write files', 'detail' => 'into public_html']],
            'conflicts' => ['Enough free disk space: 300 MB needed, 120 MB free'],
        ]);
        $this->switchTo('cust');
        $page = $this->request('GET', $this->apps($this->siteId) . '/joomla/plan')->body();
        foreach (['What installing Joomla would do', 'Nothing has been changed', 'cannot be installed yet', '300 MB needed, 120 MB free', 'Create database', 'Write files', 'GPL-2.0-or-later'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        // The plan is a read: it sent no write action, and no database was created.
        self::assertSame(0, $this->calls('apps.install'));
        self::assertSame(0, $this->calls('db.create'));
        $sent = $this->lastCall('apps.plan');
        self::assertSame('joomla', $sent['app']);
        self::assertFalse($sent['allow_testing']);
        self::assertSame('plan_preview', $sent['db']['name'], 'only placeholder names are sent');
    }

    public function testThePlanIsForTheWebsitesOwnerAndInstallableApplicationsOnly(): void
    {
        $this->agent->succeed('apps.plan', ['app' => 'joomla', 'name' => 'Joomla', 'status' => 'enabled', 'ok' => true, 'checks' => [], 'steps' => [], 'conflicts' => []]);
        $this->switchTo('cust');
        self::assertSame(404, $this->request('GET', $this->apps($this->otherSite) . '/joomla/plan')->status(), "another customer's website");
        self::assertSame(0, $this->calls('apps.plan'));
        $this->setStatus('joomla', 'draft');
        $this->request('GET', $this->apps($this->siteId) . '/joomla/plan');
        self::assertSame(0, $this->calls('apps.plan'), 'a draft application has no plan');
        $this->request('GET', $this->apps($this->siteId) . '/no-such-app/plan');
        self::assertSame(0, $this->calls('apps.plan'));
    }

    public function testCustomersCannotInstallIntoAnotherCustomersWebsite(): void
    {
        $this->switchTo('cust');
        $before = $this->calls('apps.install');
        $this->post($this->apps($this->otherSite) . '/joomla', []);
        self::assertSame($before, $this->calls('apps.install'));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM app_installs WHERE website_id = ?', [$this->otherSite]));
    }

    public function testARefusedInstallFreesTheFolderForANewAttempt(): void
    {
        $this->agent->fail('apps.install', 409, 'conflict', 'The folder public_html is not empty.');
        $this->post($this->apps($this->siteId) . '/joomla', []);
        self::assertSame('failed', $this->db->scalar("SELECT status FROM app_installs WHERE app = 'joomla'"));
        self::assertStringContainsString('not empty', (string) $this->db->scalar("SELECT error FROM app_installs WHERE app = 'joomla'"));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM databases'), 'the database made for the attempt was removed');
        // A new attempt is accepted: the failed one holds no claim.
        $this->agent->succeed('apps.install', ['job_id' => 'cccccccccccccccc']);
        $this->post($this->apps($this->siteId) . '/joomla', []);
        self::assertSame('running', $this->db->scalar("SELECT status FROM app_installs WHERE job_id = 'cccccccccccccccc'"));
    }

    public function testADatabaseFailureAlsoFreesTheFolder(): void
    {
        $this->agent->fail('db.create', 500, 'failed', 'MariaDB is not running.');
        $this->post($this->apps($this->siteId) . '/joomla', []);
        self::assertNotSame('', (string) $this->flash('error'));
        self::assertSame(0, $this->calls('apps.install'), 'the agent is not asked to install without its database');
        self::assertSame(0, (int) $this->db->scalar("SELECT COUNT(*) FROM app_installs WHERE status = 'running'"));
    }

    public function testFinishedJobsAreRecordedWithVersionAndAudited(): void
    {
        $this->post($this->apps($this->siteId) . '/joomla', []);
        $this->agent->succeed('jobs.get', ['id' => 'aaaaaaaaaaaaaaaa', 'status' => 'succeeded', 'result' => ['version' => '5.2.1']]);
        $this->request('GET', $this->apps($this->siteId));
        $row = (array) $this->db->fetch("SELECT * FROM app_installs WHERE job_id = 'aaaaaaaaaaaaaaaa'");
        self::assertSame(['succeeded', '5.2.1', 'archive'], [$row['status'], $row['version'], $row['strategy']]);
        $audit = (array) $this->db->fetch("SELECT * FROM audit_logs WHERE action = 'app.install_result' ORDER BY id DESC LIMIT 1");
        self::assertSame('success', $audit['result']);
        self::assertStringContainsString('5.2.1', (string) $audit['details']);

        // A failure is audited with the (trimmed) error and no secrets.
        $this->agent->succeed('apps.install', ['job_id' => 'dddddddddddddddd']);
        $this->post($this->apps($this->otherSite) . '/joomla', []);
        $this->agent->succeed('jobs.get', ['id' => 'dddddddddddddddd', 'status' => 'failed', 'error' => 'the Joomla download does not match its published checksum']);
        $this->request('GET', $this->apps($this->otherSite));
        self::assertSame('failed', $this->db->scalar("SELECT status FROM app_installs WHERE job_id = 'dddddddddddddddd'"));
        $fail = (array) $this->db->fetch("SELECT * FROM audit_logs WHERE action = 'app.install_result' ORDER BY id DESC LIMIT 1");
        self::assertStringContainsString('checksum', (string) $fail['details']);
        self::assertSame('failure', $fail['result']);
    }

    public function testTheDatabaseEnforcesOneRunningInstallPerFolder(): void
    {
        $now = gmdate('c');
        $this->db->run("INSERT INTO app_installs (website_id, app, path, status, created_at) VALUES (?, 'joomla', 'public_html', 'running', ?)", [$this->siteId, $now]);
        $this->expectException(\PDOException::class);
        $this->db->run("INSERT INTO app_installs (website_id, app, path, status, created_at) VALUES (?, 'drupal', 'public_html', 'running', ?)", [$this->siteId, $now]);
    }

    public function testFinishedInstallsDoNotBlockNewOnes(): void
    {
        $now = gmdate('c');
        foreach (['succeeded', 'failed', 'unknown'] as $st) {
            $this->db->run("INSERT INTO app_installs (website_id, app, path, status, created_at) VALUES (?, 'joomla', 'public_html', ?, ?)", [$this->siteId, $st, $now]);
        }
        $this->db->run("INSERT INTO app_installs (website_id, app, path, status, created_at) VALUES (?, 'joomla', 'public_html', 'running', ?)", [$this->siteId, $now]);
        self::assertSame(4, (int) $this->db->scalar('SELECT COUNT(*) FROM app_installs WHERE website_id = ?', [$this->siteId]));
    }

    public function testCatalogServiceRules(): void
    {
        $cat = $this->app->container->get(AppCatalogService::class);
        $admin = ['role_scope' => 'all', 'id' => 1];
        $customer = ['role_scope' => 'own', 'id' => 2];
        self::assertTrue($cat->installableBy('joomla', $customer));
        $this->setStatus('joomla', 'testing');
        self::assertTrue($cat->installableBy('joomla', $admin));
        self::assertFalse($cat->installableBy('joomla', $customer));
        foreach (['draft', 'deprecated', 'disabled'] as $st) {
            $this->setStatus('joomla', $st);
            self::assertFalse($cat->installableBy('joomla', $admin), $st);
        }
    }
}
