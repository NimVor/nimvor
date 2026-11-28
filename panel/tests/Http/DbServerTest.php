<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Tests\Support\HttpTestCase;

final class DbServerTest extends HttpTestCase
{
    /** @var array<string, mixed> */
    private array $status = [];

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->agent->succeed('db.server_status', $this->status = [
            'running' => true, 'unit' => 'mariadb.service', 'drop_in' => '/etc/mysql/mariadb.conf.d/zz-nimvor.cnf',
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
        // Separate pages (sidebar children of Databases).
        foreach ([
            '/databases/server' => ['10.11.14-MariaDB', '3 / 300', '2.0 /s', '60%'],
            '/databases/server/settings' => ['zz-nimvor.cnf', 'utf8mb4_unicode_ci', 'value="300"', '128 MB'],
            '/databases/server/connections' => ['SELECT * FROM orders', 'server thread'],
        ] as $path => $wants) {
            $page = $this->request('GET', $path);
            self::assertSame(200, $page->status(), $path);
            foreach ($wants as $want) {
                self::assertStringContainsString($want, $page->body(), $path . ': ' . $want);
            }
            self::assertStringContainsString('data-nim-section-menu="databases"', $page->body(), $path . ' shows the Databases menu');
        }
        self::assertStringNotContainsString('SELECT * FROM orders', $this->request('GET', '/databases/server/settings')->body());
        $this->agent->succeed('logs.sources', [['id' => 'system:mysql', 'label' => 'MariaDB error log', 'group' => 'System', 'kind' => 'file'], ['id' => 'nginx:error', 'label' => 'Nginx', 'group' => 'Web', 'kind' => 'file']]);
        $logs = $this->request('GET', '/databases/server/logs')->body();
        self::assertStringContainsString('MariaDB error log', $logs);
        self::assertStringNotContainsString('nginx:error', $logs, 'only the database logs');
        self::assertStringContainsString('/databases/server', $this->request('GET', '/databases')->body());
    }

    public function testSettingsAreCheckedByTheAgentNotByTheForm(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/databases/server/settings')->body();
        self::assertStringNotContainsString('name="managed[', $page, 'what is saved is read by the agent, never taken from the form');
        $this->agent->succeed('db.server_settings', ['changed' => ['innodb_buffer_pool_size', 'skip_name_resolve'], 'applied' => ['innodb_buffer_pool_size'], 'restart_needed' => ['skip_name_resolve']]);
        $this->post('/databases/server/settings', [
            'settings' => ['max_connections' => '300', 'innodb_buffer_pool_size' => '1G', 'skip_name_resolve' => 'ON', 'collation_server' => ''],
            'managed' => ['max_connections' => 'forged', 'innodb_buffer_pool_size' => 'forged'], // ignored
        ]);
        $call = $this->lastCall('db.server_settings');
        // Every filled field is sent (the agent compares with its file); an empty one is left alone.
        self::assertEquals(['max_connections' => '300', 'innodb_buffer_pool_size' => '1G', 'skip_name_resolve' => 'ON'], (array) $call['settings']);
        self::assertFalse($call['restart']);
        self::assertStringContainsString('skip_name_resolve take effect after', (string) $this->flash('success'));
        $db = $this->app->container->get(Connection::class);
        self::assertSame('success', $db->scalar("SELECT result FROM audit_logs WHERE action = 'database.server_settings'"));
        $audit = (string) $db->scalar("SELECT details FROM audit_logs WHERE action = 'database.server_settings' ORDER BY id DESC LIMIT 1");
        self::assertStringContainsString('skip_name_resolve', $audit);
        self::assertStringNotContainsString('max_connections', $audit, 'only what changed is audited');

        // An empty field keeps the saved value; only an explicit reset sends "" (back to the server's own value).
        $this->agent->succeed('db.server_settings', ['changed' => ['max_connections'], 'applied' => ['max_connections'], 'restart_needed' => []]);
        $this->post('/databases/server/settings', ['settings' => ['max_connections' => '', 'wait_timeout' => ''], 'reset' => ['max_connections' => '1', 'wait_timeout' => '0']]);
        self::assertEquals(['max_connections' => ''], (array) $this->lastCall('db.server_settings')['settings']);
        $calls = $this->calls('db.server_settings');
        $this->post('/databases/server/settings', ['settings' => ['max_connections' => '', 'wait_timeout' => '']]);
        self::assertSame($calls, $this->calls('db.server_settings'), 'nothing is sent when nothing is filled in or reset');
        self::assertStringContainsString('Nothing changed', (string) $this->flash('success'));

        // The agent's refusal is reported and the restart flag is passed on.
        $this->agent->fail('db.server_settings', 409, 'failed', 'The server refused innodb_buffer_pool_size; the previous settings were restored.');
        $this->post('/databases/server/settings', ['settings' => ['max_connections' => '300'], 'restart' => '1']);
        self::assertTrue($this->lastCall('db.server_settings')['restart']);
        self::assertStringContainsString('previous settings were restored', (string) $this->flash('error'));
    }

    public function testPerformanceProfilesFillTheFormAndAreOnlyOfferedToAdministrators(): void
    {
        $this->login('admin');
        $this->agent->succeed('db.server_status', array_merge($this->status, [
            'ram_bytes' => 8 * 1024 ** 3,
            'profiles' => [
                ['id' => 'small', 'name' => 'Small', 'description' => 'd', 'recommended' => false, 'values' => ['innodb_buffer_pool_size' => '1G', 'max_connections' => '100', 'innodb_io_capacity' => '400', 'innodb_io_capacity_max' => '800']],
                ['id' => 'standard', 'name' => 'Standard', 'description' => 'd', 'recommended' => true, 'values' => ['innodb_buffer_pool_size' => '3200M', 'max_connections' => '200', 'innodb_io_capacity' => '1000', 'innodb_io_capacity_max' => '2000']],
                ['id' => 'high', 'name' => 'High performance', 'description' => 'd', 'recommended' => false, 'warning' => 'Less memory than meant for.', 'values' => ['innodb_buffer_pool_size' => '4G', 'max_connections' => '400', 'innodb_io_capacity' => '2000', 'innodb_io_capacity_max' => '4000']],
            ],
        ]));
        $page = $this->request('GET', '/databases/server/settings')->body();
        self::assertStringContainsString('data-nim-db-profiles', $page);
        self::assertStringContainsString('8 GB of memory', $page);
        self::assertSame(3, substr_count($page, 'data-nim-db-profile-name='));
        self::assertStringContainsString('Recommended here', $page);
        self::assertStringContainsString('Less memory than meant for.', $page);
        self::assertStringContainsString('Workload check', $page);
        self::assertStringContainsString('data-nim-db-profile-status', $page);
        foreach (['Detected memory', '10.11.14-MariaDB', 'Running for', 'reset whenever MariaDB restarts', 'Too early to judge', 'at least 24 hours', 'Right now (a snapshot)', 'needs restart', 'data-nim-db-reset-flag'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }
        // The same window version, and nothing is saved by showing it.
        self::assertStringContainsString('data-nim-db-profiles', $this->request('GET', '/databases/server/settings/modal')->body());
        self::assertSame(0, $this->calls('db.server_settings'));
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
