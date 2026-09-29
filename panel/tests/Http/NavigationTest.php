<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Http\Session\ArraySession;
use WhaCP\Tests\Support\HttpTestCase;

/** Sidebar, global search (Ctrl+K) and dashboards: each role sees what it may open. */
final class NavigationTest extends HttpTestCase
{
    private function as(string $who): void
    {
        $this->session = new ArraySession();
        $this->app = $this->makeApp();
        $this->login($who);
    }

    public function testAdministratorsGetSearchAndTheToolsGrid(): void
    {
        $this->seedUser('admin', 'admin');
        $this->agent->succeed('system.info', ['system' => ['hostname' => 'srv1', 'uptime_seconds' => 60], 'os' => ['pretty_name' => 'Ubuntu 24.04', 'supported' => true]]);
        $this->login('admin');
        $page = $this->request('GET', '/')->body();
        foreach (['id="wcp-palette"', 'data-wcp-palette-item', 'Tools &amp; settings', 'Create website', 'wcp-hero', 'srv1', 'System accounts', 'wcp-sidebar-account'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
    }

    public function testResellersSeeTheirAccountNotTheServer(): void
    {
        $this->seedUser('admin', 'admin');
        $res = $this->seedUser('res', 'reseller');
        $cust = $this->seedUser('cust', 'customer');
        $db = $this->app->container->get(Connection::class);
        $db->run('UPDATE users SET parent_id = ? WHERE id = ?', [$res, $cust]);
        $db->run("INSERT INTO websites (server_id, domain, kind, php_version, https, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, 'cust.test', 'php', '8.3', 1, '/home/whacp/cust.test', '/home/whacp/cust.test/public_html', 'cust', ?, 'active', 'x', 'x')", [$cust]);
        $this->agent->succeed('system.info', ['system' => ['hostname' => 'secret-host'], 'os' => []]);
        $this->as('res');
        $page = $this->request('GET', '/')->body();
        foreach (['Reseller account', 'cust.test', 'cust@example.test', 'Plan use', 'New account'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        self::assertStringNotContainsString('secret-host', $page, 'no server details for resellers');
        self::assertSame(0, $this->calls('system.info'));
        foreach (['/system-users', '/packages', '/settings', '>Web terminal<'] as $never) {
            self::assertStringNotContainsString($never, $page, $never . ' is not offered to resellers');
        }
    }

    public function testCustomersKeepTheSimpleLayout(): void
    {
        $this->seedUser('cust', 'customer');
        $this->login('cust');
        self::assertSame('/websites', $this->request('GET', '/')->header('Location'));
        self::assertStringNotContainsString('wcp-palette', $this->request('GET', '/websites')->body());
    }
}
