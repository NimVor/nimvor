<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Tests\Support\HttpTestCase;

final class DbServerTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->agent->succeed('db.server_status', [
            'running' => true, 'unit' => 'mariadb.service', 'drop_in' => '/etc/mysql/mariadb.conf.d/zz-whacp.cnf',
            'managed' => ['max_connections' => '300'],
            'settings' => [
                ['key' => 'max_connections', 'label' => 'Maximum connections', 'help' => 'h', 'kind' => 'int', 'dynamic' => true, 'group' => 'Connections'],
                ['key' => 'innodb_buffer_pool_size', 'label' => 'InnoDB buffer pool', 'help' => 'h', 'kind' => 'size', 'dynamic' => true, 'group' => 'Memory and storage'],
                ['key' => 'skip_name_resolve', 'label' => 'Skip host name lookups', 'help' => 'h', 'kind' => 'bool', 'dynamic' => false, 'group' => 'Connections'],
                ['key' => 'collation_server', 'label' => 'Default collation', 'help' => 'h', 'kind' => 'choice', 'dynamic' => true, 'group' => 'Character set'],
            ],
            'variables' => ['version' => '10.11.14-MariaDB', 'version_comment' => 'Ubuntu 24.04', 'max_connections' => '300', 'innodb_buffer_pool_size' => '134217728', 'socket' => '/run/mysqld/mysqld.sock', 'port' => '3306', 'bind_address' => '127.0.0.1', 'character_set_server' => 'utf8mb4', 'collation_server' => 'utf8mb4_general_ci'],
            'status' => ['Uptime' => '3600', 'Threads_connected' => '3', 'Questions' => '7200', 'Innodb_buffer_pool_pages_total' => '100', 'Innodb_buffer_pool_pages_free' => '40', 'Innodb_buffer_pool_read_requests' => '1000', 'Innodb_buffer_pool_reads' => '5'],
            'collations' => ['utf8mb4' => ['utf8mb4_general_ci', 'utf8mb4_unicode_ci']],
            'databases' => 4, 'users' => 5, 'bytes' => 1048576,
        ]);
        $this->agent->succeed('db.processlist', ['connections' => [
            ['id' => 7, 'user' => 'shop', 'host' => 'localhost', 'db' => 'shop', 'command' => 'Query', 'time' => 12, 'state' => 'Sending data', 'info' => 'SELECT * FROM orders', 'system' => false],
            ['id' => 1, 'user' => 'system user', 'host' => '', 'db' => '', 'command' => 'Daemon', 'time' => 99, 'state' => '', 'info' => '', 'system' => true],
        ]]);
    }

    public function testAdministratorSeesStatusSettingsAndConnections(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/databases/server');
        self::assertSame(200, $page->status());
        $body = $page->body();
        foreach (['10.11.14-MariaDB', '3 / 300', '2.0 /s', '60%', 'zz-whacp.cnf', 'SELECT * FROM orders', 'server thread', 'utf8mb4_unicode_ci', 'value="300"', '128 MB'] as $want) {
            self::assertStringContainsString($want, $body, $want);
        }
        self::assertStringContainsString('/databases/server', $this->request('GET', '/databases')->body());
    }

    public function testSettingsSendOnlyChangesAndAreAudited(): void
    {
        $this->login('admin');
        $this->agent->succeed('db.server_settings', ['changed' => ['innodb_buffer_pool_size', 'skip_name_resolve'], 'applied' => ['innodb_buffer_pool_size'], 'restart_needed' => ['skip_name_resolve']]);
        $this->post('/databases/server/settings', [
            'settings' => ['max_connections' => '300', 'innodb_buffer_pool_size' => '1G', 'skip_name_resolve' => 'ON', 'collation_server' => ''],
            'managed' => ['max_connections' => '300', 'innodb_buffer_pool_size' => '', 'skip_name_resolve' => '', 'collation_server' => ''],
        ]);
        $call = $this->lastCall('db.server_settings');
        self::assertEquals(['innodb_buffer_pool_size' => '1G', 'skip_name_resolve' => 'ON'], (array) $call['settings']);
        self::assertFalse($call['restart']);
        self::assertStringContainsString('skip_name_resolve take effect after', (string) $this->flash('success'));
        $db = $this->app->container->get(Connection::class);
        self::assertSame('success', $db->scalar("SELECT result FROM audit_logs WHERE action = 'database.server_settings'"));

        // Clearing a managed value sends "" (back to the server's own value); a refusal is reported.
        $this->agent->fail('db.server_settings', 409, 'failed', 'The server refused innodb_buffer_pool_size; the previous settings were restored.');
        $this->post('/databases/server/settings', ['settings' => ['max_connections' => ''], 'managed' => ['max_connections' => '300'], 'restart' => '1']);
        $call = $this->lastCall('db.server_settings');
        self::assertEquals(['max_connections' => ''], (array) $call['settings']);
        self::assertTrue($call['restart']);
        self::assertStringContainsString('previous settings were restored', (string) $this->flash('error'));
    }

    public function testRestartFlushAndKill(): void
    {
        $this->login('admin');
        $this->agent->succeed('db.server_restart', ['restarted' => true, 'seconds' => 4]);
        $this->post('/databases/server/restart', []);
        self::assertStringContainsString('restarted in 4 s', (string) $this->flash('success'));
        $this->agent->succeed('db.server_flush', ['flushed' => 'hosts']);
        $this->post('/databases/server/flush', ['what' => 'hosts']);
        self::assertSame('hosts', $this->lastCall('db.server_flush')['what']);
        $this->agent->succeed('db.kill', ['killed' => 7]);
        $this->post('/databases/server/kill', ['id' => '7', 'query_only' => '1']);
        self::assertSame(['id' => 7, 'query_only' => true], $this->lastCall('db.kill'));
        $db = $this->app->container->get(Connection::class);
        self::assertSame(3, (int) $db->scalar("SELECT COUNT(*) FROM audit_logs WHERE action IN ('database.server_restart', 'database.server_flush', 'database.kill')"));
    }

    public function testCustomersCannotManageTheServer(): void
    {
        $this->login('cust');
        self::assertNotSame(200, $this->request('GET', '/databases/server')->status());
        foreach (['/databases/server/settings' => ['settings' => ['max_connections' => '10']], '/databases/server/restart' => [], '/databases/server/kill' => ['id' => '7'], '/databases/server/flush' => ['what' => 'hosts']] as $url => $body) {
            $this->post($url, $body);
        }
        foreach (['db.server_settings', 'db.server_restart', 'db.kill', 'db.server_flush'] as $a) {
            self::assertSame(0, $this->calls($a), $a);
        }
        self::assertStringNotContainsString('/databases/server', $this->request('GET', '/databases')->body());
    }
}
