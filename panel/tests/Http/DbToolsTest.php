<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Tests\Support\HttpTestCase;

/** phpMyAdmin and Adminer, installed by an administrator next to the built-in database browser. */
final class DbToolsTest extends HttpTestCase
{
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $cust = $this->seedUser('cust', 'customer');
        $this->db = $this->app->container->get(Connection::class);
        $server = (int) $this->db->scalar('SELECT id FROM servers ORDER BY id LIMIT 1');
        $this->db->run("INSERT INTO databases (server_id, name, owner_id, created_at) VALUES (?, 'cust_shop', ?, '2026-01-01')", [$server, $cust]);
        $this->agent->succeed('dbtools.install', ['job_id' => 'aaaaaaaaaaaaaaaa']);
        $this->agent->succeed('dbtools.remove', ['removed' => true]);
        $this->agent->succeed('dbtools.signon', ['path' => '/dbtools/phpmyadmin/nimvor-signon.php?t=' . str_repeat('ab', 16), 'expires_in' => 60]);
        $this->agent->succeed('dbtools.signon_sweep', ['locked' => 2, 'failed' => 0, 'tokens_removed' => 0]);
    }

    /** @param list<string> $installed */
    private function toolsStatus(array $installed, bool $https = true): void
    {
        $tools = [];
        foreach (['phpmyadmin' => 'phpMyAdmin', 'adminer' => 'Adminer'] as $id => $name) {
            $tools[] = ['id' => $id, 'name' => $name, 'description' => 'A database manager.', 'installed' => in_array($id, $installed, true), 'installed_at' => '', 'path' => '/dbtools/' . $id . '/'];
        }
        $this->agent->succeed('dbtools.status', ['tools' => $tools, 'php' => '8.3', 'panel_https' => $https]);
    }

    public function testAdministratorInstallsAndRemoves(): void
    {
        $this->toolsStatus([]);
        $this->login('admin');
        $page = $this->request('GET', '/databases/tools')->body();
        self::assertStringContainsString('Not installed', $page);
        self::assertStringContainsString('/databases/tools/phpmyadmin/install', $page);

        $this->post('/databases/tools/phpmyadmin/install');
        self::assertSame('phpmyadmin', $this->lastCall('dbtools.install')['tool']);
        self::assertStringContainsString('Installing phpMyAdmin', (string) $this->flash('success'));

        $this->toolsStatus(['phpmyadmin']);
        $page = $this->request('GET', '/databases/tools')->body();
        self::assertStringContainsString('Installed', $page);
        self::assertStringContainsString('/databases/tools/phpmyadmin/remove', $page);
        $this->post('/databases/tools/phpmyadmin/remove');
        self::assertSame('phpmyadmin', $this->lastCall('dbtools.remove')['tool']);
    }

    public function testOnlyKnownToolsAndOnlyAdministrators(): void
    {
        $this->toolsStatus([]);
        $this->login('admin');
        $before = $this->calls('dbtools.install');
        $this->post('/databases/tools/evil/install');
        self::assertSame($before, $this->calls('dbtools.install'), 'an unknown tool never reaches the agent');

        $this->session = new \NimVor\Http\Session\ArraySession();
        $this->login('cust');
        $this->post('/databases/tools/phpmyadmin/install');
        $this->post('/databases/tools/phpmyadmin/remove');
        self::assertSame($before, $this->calls('dbtools.install'));
        self::assertSame(0, $this->calls('dbtools.remove'));
        $page = $this->request('GET', '/databases/tools')->body();
        self::assertStringNotContainsString('/databases/tools/phpmyadmin/install', $page, 'a customer sees no install button');
    }

    public function testInstalledToolsAreOfferedNextToTheBuiltInBrowser(): void
    {
        $this->toolsStatus([]);
        $this->login('cust');
        $none = $this->request('GET', '/databases')->body();
        self::assertStringContainsString('Browse', $none);
        self::assertStringNotContainsString('/tools/phpmyadmin/open', $none);

        $this->toolsStatus(['phpmyadmin', 'adminer']);
        $page = $this->request('GET', '/databases')->body();
        self::assertMatchesRegularExpression('#<form method="post" action="/databases/\d+/tools/phpmyadmin/open" target="_blank"#', $page);
        self::assertMatchesRegularExpression('#action="/databases/\d+/tools/adminer/open"#', $page);
        self::assertStringContainsString('Browse', $page, 'the built-in browser stays');
    }

    public function testOneClickSignInSendsTheBrowserToTheToolForThatDatabase(): void
    {
        $this->toolsStatus(['phpmyadmin']);
        $this->login('cust');
        $id = (int) $this->db->scalar("SELECT id FROM databases WHERE name = 'cust_shop'");
        $res = $this->post('/databases/' . $id . '/tools/phpmyadmin/open');
        self::assertSame('cust_shop', $this->lastCall('dbtools.signon')['database']);
        self::assertSame('phpmyadmin', $this->lastCall('dbtools.signon')['tool']);
        self::assertSame('/dbtools/phpmyadmin/nimvor-signon.php?t=' . str_repeat('ab', 16), $res->header('Location'));
    }

    public function testSignInIsRefusedForAnotherUsersDatabaseAndAnUnknownTool(): void
    {
        $this->toolsStatus(['phpmyadmin']);
        $server = (int) $this->db->scalar('SELECT id FROM servers ORDER BY id LIMIT 1');
        $this->db->run("INSERT INTO databases (server_id, name, owner_id, created_at) VALUES (?, 'admin_crm', NULL, '2026-01-01')", [$server]);
        $other = (int) $this->db->scalar("SELECT id FROM databases WHERE name = 'admin_crm'");
        $mine = (int) $this->db->scalar("SELECT id FROM databases WHERE name = 'cust_shop'");
        $this->login('cust');
        $before = $this->calls('dbtools.signon');
        $this->post('/databases/' . $other . '/tools/phpmyadmin/open');
        $this->post('/databases/' . $mine . '/tools/evil/open');
        self::assertSame($before, $this->calls('dbtools.signon'), 'neither request reaches the agent');
    }

    public function testSchedulerLocksExpiredSignInAccountsOnlyWhenToolsAreInstalled(): void
    {
        $service = $this->app->container->get(\NimVor\Services\DbToolsService::class);
        $this->toolsStatus([]);
        $before = $this->calls('dbtools.signon_sweep');
        self::assertSame('', $service->sweep());
        self::assertSame($before, $this->calls('dbtools.signon_sweep'), 'nothing installed: nothing asked');
        $this->toolsStatus(['adminer']);
        self::assertStringContainsString('locked 2', $service->sweep());
    }
}
