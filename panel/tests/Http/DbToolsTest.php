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
        self::assertStringNotContainsString('href="/dbtools/phpmyadmin/"', $none);

        $this->toolsStatus(['phpmyadmin', 'adminer']);
        $page = $this->request('GET', '/databases')->body();
        self::assertStringContainsString('href="/dbtools/phpmyadmin/"', $page);
        self::assertStringContainsString('href="/dbtools/adminer/"', $page);
        self::assertStringContainsString('Browse', $page, 'the built-in browser stays');
    }
}
