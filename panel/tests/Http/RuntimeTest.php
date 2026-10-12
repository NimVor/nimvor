<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Database\Connection;
use WhaCP\Tests\Support\HttpTestCase;

/** Server → Runtimes: install/remove Node.js, admin only. */
final class RuntimeTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->agent->succeed('runtime.status', [
            'arch' => 'x64',
            'node' => [['kind' => 'node', 'version' => '20.17.0', 'bin' => '/opt/whacp/runtimes/node/20.17.0/bin', 'binary' => '/opt/whacp/runtimes/node/20.17.0/bin/node', 'managed' => true]],
            'python' => [],
            'system' => [['kind' => 'python', 'version' => '3.12', 'binary' => '/usr/bin/python3.12']],
        ]);
    }

    public function testAdminSeesAndInstallsAndRemovesRuntimes(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/runtimes')->body();
        foreach (['Runtimes', '20.17.0', '3.12', 'Install Node.js'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }

        $this->agent->succeed('runtime.install', ['job_id' => 'abcdefabcdefabcd']);
        $this->post('/runtimes/install', ['version' => '22.9.0']);
        self::assertSame(['kind' => 'node', 'version' => '22.9.0'], $this->lastCall('runtime.install'));
        self::assertSame(1, (int) $this->app->container->get(Connection::class)->scalar("SELECT COUNT(*) FROM agent_jobs WHERE job_id = 'abcdefabcdefabcd'"));

        // A bad version never reaches the agent.
        $this->post('/runtimes/install', ['version' => 'latest']);
        self::assertSame(1, $this->calls('runtime.install'));

        $this->agent->succeed('runtime.remove', ['removed' => 'node:20.17.0']);
        $this->post('/runtimes/remove', ['kind' => 'node', 'version' => '20.17.0']);
        self::assertSame(['kind' => 'node', 'version' => '20.17.0'], $this->lastCall('runtime.remove'));
        self::assertSame(1, (int) $this->app->container->get(Connection::class)->scalar("SELECT COUNT(*) FROM audit_logs WHERE action = 'runtime.remove'"));
    }

    public function testCustomersCannotReachRuntimeManagement(): void
    {
        $this->login('cust');
        self::assertSame(403, $this->request('GET', '/runtimes')->status());
        self::assertSame(403, $this->post('/runtimes/install', ['version' => '20.17.0'])->status());
        self::assertSame(0, $this->calls('runtime.install'));
    }
}
