<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Http\Session\ArraySession;
use NimVor\Tests\Support\HttpTestCase;

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
        foreach (['id="nim-palette"', 'data-nim-palette-item', 'Tools &amp; settings', 'Create website', 'nim-hero', 'srv1', 'System accounts', 'data-nim-nav-toggle="Hosting"', 'AutoSSL'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        // The account menu is in the top bar only.
        self::assertStringNotContainsString('nim-sidebar-account', $page);
    }

    public function testResourceLimitsAreUnderAdministration(): void
    {
        $groups = \NimVor\View\Navigation::groups();
        $keys = static fn (string $g): array => array_map(static fn (array $i): string => (string) $i[2], $groups[$g]);
        self::assertContains('limits', $keys('Administration'));
        self::assertNotContains('limits', $keys('Server'));
    }

    public function testResellersSeeTheirAccountNotTheServer(): void
    {
        $this->seedUser('admin', 'admin');
        $res = $this->seedUser('res', 'reseller');
        $cust = $this->seedUser('cust', 'customer');
        $db = $this->app->container->get(Connection::class);
        $db->run('UPDATE users SET parent_id = ? WHERE id = ?', [$res, $cust]);
        $db->run("INSERT INTO websites (server_id, domain, kind, php_version, https, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, 'cust.test', 'php', '8.3', 1, '/home/nimvor/cust.test', '/home/nimvor/cust.test/public_html', 'cust', ?, 'active', 'x', 'x')", [$cust]);
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
        self::assertStringNotContainsString('nim-palette', $this->request('GET', '/websites')->body());
    }

    public function testSectionMenusMarkTheCurrentPage(): void
    {
        $this->seedUser('admin', 'admin');
        $this->login('admin');
        foreach ([
            '/mail/admin/queue' => ['mail', 'Mail queue'],
            '/mail/admin/reports' => ['mail', 'Delivery reports'],
            '/security/firewall' => ['security', 'Firewall'],
            '/databases/server/connections' => ['databases', 'Database connections'],
        ] as $path => [$menu, $label]) {
            $body = $this->request('GET', $path)->body();
            self::assertMatchesRegularExpression('#data-nim-section-menu="' . $menu . '".*?class="nim-subnav-link active"[^>]*aria-current="page"><span>' . preg_quote($label, '#') . '</span>#s', $body, $path);
        }
        $stats = $this->request('GET', '/mail/admin/reports', [], [], '', ['view' => 'stats'])->body();
        self::assertMatchesRegularExpression('#nim-subnav-link active"[^>]*><span>Statistics</span>#', $stats);
        self::assertDoesNotMatchRegularExpression('#nim-subnav-link active"[^>]*><span>Delivery reports</span>#', $stats);
        self::assertStringNotContainsString('nim-pagetabs', $this->request('GET', '/security/ssh')->body(), 'no tab bar: the sidebar menu replaces it');
    }
}
