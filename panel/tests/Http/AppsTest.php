<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

final class AppsTest extends HttpTestCase
{
    private int $siteId;
    private int $adminId;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->adminId = $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->siteId = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3'], (array) $this->app->container->get(UserRepository::class)->find($this->adminId));
        $this->db = $this->app->container->get(Connection::class);
        foreach (['db.create', 'db.user_create', 'db.grant', 'db.drop', 'db.user_drop'] as $a) {
            $this->agent->succeed($a, ['ok' => true]);
        }
    }

    public function testWordPressInstall(): void
    {
        $this->agent->succeed('apps.install', ['job_id' => 'aaaaaaaaaaaaaaaa']);
        $base = '/sites/' . $this->siteId . '/apps';
        self::assertStringContainsString('Install WordPress', $this->request('GET', $base)->body());

        $this->post($base . '/wordpress', ['title' => 'Shop', 'admin_user' => 'owner', 'admin_email' => 'nope']);
        self::assertArrayHasKey('admin_email', (array) $this->flash('errors'));
        self::assertSame(0, $this->calls('db.create'), 'nothing is created when input is invalid');

        $this->post($base . '/wordpress', ['title' => 'Shop', 'admin_user' => 'owner', 'admin_email' => 'owner@shop.test']);
        $call = $this->lastCall('apps.install');
        self::assertSame('public_html', $call['path']);
        self::assertSame('shop_wp', $call['db']['name']);
        self::assertSame('http://shop.test', $call['url']);
        $secret = (array) $this->flash('secret');
        self::assertSame($call['admin_password'], $secret['password']);
        self::assertGreaterThanOrEqual(20, strlen($secret['password']));
        self::assertStringNotContainsString($secret['password'], (string) json_encode($this->db->fetchAll('SELECT * FROM audit_logs')));
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM app_installs WHERE app = 'wordpress'"));

        // The same folder cannot be installed into twice at once: the repeat is
        // refused without creating a second database or starting a second job.
        $dbs = (int) $this->db->scalar('SELECT COUNT(*) FROM databases');
        $jobs = $this->calls('apps.install');
        $this->post($base . '/wordpress', ['title' => 'Shop', 'admin_user' => 'owner', 'admin_email' => 'owner@shop.test']);
        self::assertStringContainsString('already in progress', (string) $this->flash('error'));
        self::assertSame($dbs, (int) $this->db->scalar('SELECT COUNT(*) FROM databases'), 'no second database');
        self::assertSame($jobs, $this->calls('apps.install'), 'no second job');
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM app_installs WHERE app = 'wordpress'"));

        // Once the first has finished, a second WordPress gets its own database name.
        $this->db->run("UPDATE app_installs SET status = 'succeeded' WHERE status = 'running'");
        $this->post($base . '/wordpress', ['title' => 'Shop', 'admin_user' => 'owner', 'admin_email' => 'owner@shop.test']);
        self::assertSame('shop_wp1', $this->lastCall('apps.install')['db']['name']);
    }

    public function testWooCommerceIsWordPressWithThePlugin(): void
    {
        $this->agent->succeed('apps.install', ['job_id' => 'cccccccccccccccc']);
        $base = '/sites/' . $this->siteId . '/apps';
        self::assertStringContainsString('Install WooCommerce', $this->request('GET', $base)->body());
        $this->post($base . '/woocommerce', ['title' => 'Shop', 'admin_user' => 'owner', 'admin_email' => 'owner@shop.test']);
        $call = $this->lastCall('apps.install');
        self::assertSame('wordpress', $call['app']);
        self::assertSame(['woocommerce'], $call['plugins']);
        self::assertSame('shop_wp', $call['db']['name']);
        // Recorded as WordPress, so the WordPress tools and login link apply.
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM app_installs WHERE app = 'wordpress'"));
    }

    public function testStagedInstallCreatesDatabaseAndOffersFinish(): void
    {
        $this->agent->succeed('apps.install', ['job_id' => 'bbbbbbbbbbbbbbbb']);
        $base = '/sites/' . $this->siteId . '/apps';
        $page = $this->request('GET', $base)->body();
        self::assertStringContainsString('Joomla', $page);
        self::assertStringContainsString('data-app-cat', $page, 'the category sidebar renders');

        $this->post($base . '/joomla', []);
        $call = $this->lastCall('apps.install');
        self::assertSame('joomla', $call['app']);
        self::assertSame('public_html', $call['path'], 'staged apps install into the document root');
        self::assertArrayHasKey('name', $call['db'], 'a database was created for the staged app');
        self::assertSame(1, (int) $this->db->scalar("SELECT COUNT(*) FROM app_installs WHERE app = 'joomla'"));

        // Once the job succeeds, the page offers the web-installer handoff.
        $this->db->run("UPDATE app_installs SET status = 'succeeded' WHERE app = 'joomla'");
        self::assertStringContainsString('shop.test/installation/', $this->request('GET', $base)->body());
    }

    public function testUnknownAppIsRejected(): void
    {
        $this->post('/sites/' . $this->siteId . '/apps/magento', []);
        self::assertSame(0, $this->calls('apps.install'));
    }

    public function testRefusalRemovesTheDatabase(): void
    {
        $this->agent->fail('apps.install', 409, 'conflict', 'The folder public_html is not empty.');
        $this->post('/sites/' . $this->siteId . '/apps/wordpress', ['admin_user' => 'owner', 'admin_email' => 'owner@shop.test']);
        self::assertStringContainsString('not empty', (string) $this->flash('error'));
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM databases'), 'the database created for the install is removed');
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM database_users'));
        self::assertSame(1, $this->calls('db.drop'));
    }

    public function testLaravelSetsTheDocumentRootWhenDone(): void
    {
        $this->agent->succeed('apps.install', ['job_id' => 'bbbbbbbbbbbbbbbb']);
        $base = '/sites/' . $this->siteId . '/apps';
        $this->post($base . '/laravel', ['path' => '../etc']);
        self::assertArrayHasKey('path', (array) $this->flash('errors'));
        $this->post($base . '/laravel', ['path' => 'app', 'migrate' => '1']);
        self::assertTrue($this->lastCall('apps.install')['migrate']);
        $this->agent->succeed('jobs.get', ['id' => 'bbbbbbbbbbbbbbbb', 'status' => 'succeeded', 'result' => ['document_root' => 'app/public']]);
        $this->request('GET', $base);
        self::assertSame('/home/nimvor/shop.test/app/public', $this->db->scalar('SELECT document_root FROM websites WHERE id = ?', [$this->siteId]));
        self::assertSame('succeeded', $this->db->scalar('SELECT status FROM app_installs'));
    }

    public function testInstallOnASubdomainTarget(): void
    {
        // A subdomain of the website: the Apps page offers it as an install
        // target, and the install runs against the subdomain's own root.
        $sub = $this->app->container->get(WebsiteService::class)->createChild(
            $this->app->container->get(\NimVor\Repositories\WebsiteRepository::class)->find(1, $this->siteId, null),
            'subdomain',
            ['subdomain' => 'blog'],
            (array) $this->app->container->get(UserRepository::class)->find($this->adminId)
        )['id'];

        $base = '/sites/' . $this->siteId . '/apps';
        $page = $this->request('GET', $base)->body();
        self::assertStringContainsString('Install on', $page, 'the target selector appears once a subdomain exists');
        self::assertStringContainsString('blog.shop.test', $page);

        $this->agent->succeed('apps.install', ['job_id' => 'cccccccccccccccc']);
        $this->post($base . '/wordpress', ['title' => 'Blog', 'admin_user' => 'owner', 'admin_email' => 'owner@shop.test', 'target_id' => (string) $sub]);
        $call = $this->lastCall('apps.install');
        self::assertSame('blog.shop.test', $call['domain'], 'the install targets the chosen subdomain');
        self::assertSame('http://blog.shop.test', $call['url']);
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM app_installs WHERE app = ? AND website_id = ?', ['wordpress', $sub]));
    }

    public function testStaticSitesAreRefused(): void
    {
        $this->db->run("UPDATE websites SET kind = 'static', php_version = '' WHERE id = ?", [$this->siteId]);
        $this->post('/sites/' . $this->siteId . '/apps/wordpress', ['admin_user' => 'owner', 'admin_email' => 'owner@shop.test']);
        self::assertStringContainsString('needs a PHP website', (string) $this->flash('error'));
    }
}
