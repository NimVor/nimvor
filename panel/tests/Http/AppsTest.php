<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;
use WhaCP\Services\WebsiteService;
use WhaCP\Tests\Support\HttpTestCase;

final class AppsTest extends HttpTestCase
{
    private int $siteId;
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $admin = $this->seedUser('admin', 'admin');
        $this->login('admin');
        $this->siteId = $this->app->container->get(WebsiteService::class)->create(['domain' => 'shop.test', 'kind' => 'php', 'php_version' => '8.3'], (array) $this->app->container->get(UserRepository::class)->find($admin));
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

        // A second WordPress gets its own database name.
        $this->post($base . '/wordpress', ['title' => 'Shop', 'admin_user' => 'owner', 'admin_email' => 'owner@shop.test']);
        self::assertSame('shop_wp1', $this->lastCall('apps.install')['db']['name']);
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
        self::assertSame('/home/whacp/shop.test/app/public', $this->db->scalar('SELECT document_root FROM websites WHERE id = ?', [$this->siteId]));
        self::assertSame('succeeded', $this->db->scalar('SELECT status FROM app_installs'));
    }

    public function testStaticSitesAreRefused(): void
    {
        $this->db->run("UPDATE websites SET kind = 'static', php_version = '' WHERE id = ?", [$this->siteId]);
        $this->post('/sites/' . $this->siteId . '/apps/wordpress', ['admin_user' => 'owner', 'admin_email' => 'owner@shop.test']);
        self::assertStringContainsString('needs a PHP website', (string) $this->flash('error'));
    }
}
