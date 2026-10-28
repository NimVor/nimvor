<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Services\FeatureService;
use NimVor\Tests\Support\HttpTestCase;

/** Node.js / Python first-class site kinds: runtime version + app. */
final class SiteRuntimeTest extends HttpTestCase
{
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $this->agent->succeed('sites.apply', ['changed' => []]);
        $this->agent->succeed('workers.apply', ['ok' => true]);
        $this->agent->succeed('runtime.status', [
            'arch' => 'x64',
            'node' => [['version' => '20.1.0'], ['version' => '22.0.0']],
            'python' => [['version' => '3.12.1']],
            'system' => [],
        ]);
    }

    public function testCreatingANodeSiteMapsToProxyAndStartsTheApp(): void
    {
        $this->login('admin');
        $this->post('/websites/new', [
            'domain' => 'app.test', 'kind' => 'nodejs', 'app_runtime' => 'node:20.1.0',
            'app_port' => '3000', 'app_command' => 'node server.js',
        ]);
        $site = (array) $this->db->fetch("SELECT * FROM websites WHERE domain = 'app.test'");
        self::assertNotSame([], $site);
        self::assertSame('proxy', $site['kind'], 'the agent sees a reverse proxy');
        self::assertSame('node:20.1.0', $site['app_runtime']);
        self::assertSame('http://127.0.0.1:3000', $site['proxy_pass']);

        // The app was started as a Worker on the chosen runtime.
        $worker = (array) $this->db->fetch('SELECT * FROM workers WHERE website_id = ? AND name = ?', [(int) $site['id'], 'app']);
        self::assertSame('node:20.1.0', $worker['runtime']);
        self::assertSame('node server.js', $worker['command']);

        // The runtime page shows the version and the app.
        $page = $this->request('GET', '/sites/' . $site['id'] . '/nodejs')->body();
        self::assertStringContainsString('Node.js version', $page);
        self::assertStringContainsString('node server.js', $page);

        // Changing the version updates the site and re-applies the app worker.
        $before = $this->calls('workers.apply');
        $this->post('/sites/' . $site['id'] . '/nodejs', ['app_runtime' => 'node:22.0.0']);
        self::assertSame('node:22.0.0', $this->db->scalar('SELECT app_runtime FROM websites WHERE id = ?', [(int) $site['id']]));
        self::assertSame('node:22.0.0', $this->db->scalar('SELECT runtime FROM workers WHERE id = ?', [(int) $worker['id']]));
        self::assertGreaterThan($before, $this->calls('workers.apply'));
    }

    public function testNodeTypeAndRuntimePageAreSeparatelyGated(): void
    {
        // A plan that allows creating Node.js sites but not the runtime page.
        $this->db->run("INSERT INTO plans (name, created_at, updated_at) VALUES ('P', 'x', 'x')");
        $plan = (int) $this->db->lastInsertId();
        $features = array_values(array_diff(array_keys(FeatureService::FEATURES), ['nodejs']));
        $list = $this->app->container->get(FeatureService::class)->save(null, ['name' => 'NodeNoPage', 'features' => $features], 1);
        $this->db->run('UPDATE plans SET feature_list_id = ? WHERE id = ?', [$list, $plan]);
        $this->db->run("UPDATE users SET plan_id = ? WHERE username = 'cust'", [$plan]);
        $this->login('cust');

        $this->post('/websites/new', ['domain' => 'napp.test', 'kind' => 'nodejs', 'app_runtime' => 'node:20.1.0', 'app_port' => '3000']);
        $id = (int) $this->db->scalar("SELECT id FROM websites WHERE domain = 'napp.test'");
        self::assertGreaterThan(0, $id, 'the customer may create a Node.js site');
        self::assertSame(403, $this->request('GET', '/sites/' . $id . '/nodejs')->status(), 'but the runtime page needs the nodejs feature');
    }
}
