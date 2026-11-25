<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Tests\Support\HttpTestCase;

/** The details behind the CPU, memory, disk and load tiles. */
final class DashboardPopoverTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->agent->succeed('system.live', [
            'cpu' => ['model' => 'Test CPU 9000', 'sockets' => 1, 'cores' => 4, 'threads' => 8, 'percent' => 42.5, 'per_core' => [['cpu' => 'cpu0', 'percent' => 10.0], ['cpu' => 'cpu1', 'percent' => 75.5]]],
            'memory' => ['total_bytes' => 8589934592, 'used_bytes' => 4294967296, 'available_bytes' => 4294967296, 'used_percent' => 50.0, 'swap_total_bytes' => 0, 'swap_used_bytes' => 0],
            'load' => ['load1' => 1.5, 'load5' => 1.0, 'load15' => 0.5],
            'processes' => 120, 'running' => 2, 'zombies' => 0,
            'top_cpu' => [['pid' => 7, 'name' => 'php-fpm', 'user' => 'www', 'cpu_percent' => 12.3, 'rss_bytes' => 1048576]],
            'top_memory' => [['pid' => 9, 'name' => 'mariadbd', 'user' => 'mysql', 'cpu_percent' => 1.0, 'rss_bytes' => 536870912]],
        ]);
        $this->agent->succeed('disk.filesystems', [['mount' => '/', 'device' => '/dev/vda1', 'type' => 'ext4', 'total_bytes' => 100000000000, 'used_bytes' => 50000000000, 'avail_bytes' => 50000000000, 'used_percent' => 50.0, 'inodes' => 100, 'inodes_used' => 10, 'inodes_percent' => 10.0]]);
        $this->login('admin');
    }

    public function testTheTilesCarryTheirPopoverAddress(): void
    {
        $page = $this->request('GET', '/')->body();
        foreach (['cpu', 'mem', 'disk', 'load'] as $key) {
            self::assertStringContainsString('data-popover-url="/dashboard/popover/' . $key . '"', $page);
        }
    }

    public function testTheCpuPopoverListsTheProcessorEachCoreAndTheBusiestProcesses(): void
    {
        $res = json_decode($this->request('GET', '/dashboard/popover/cpu')->body(), true);
        self::assertTrue($res['success']);
        self::assertStringContainsString('Test CPU 9000', $res['html']);
        self::assertStringContainsString('Core 1', $res['html']);
        self::assertStringContainsString('75.5%', $res['html']);
        self::assertStringContainsString('php-fpm', $res['html']);
    }

    public function testTheMemoryLoadAndDiskPopoversShowTheirFigures(): void
    {
        self::assertStringContainsString('mariadbd', json_decode($this->request('GET', '/dashboard/popover/mem')->body(), true)['html']);
        self::assertStringContainsString('1.50', json_decode($this->request('GET', '/dashboard/popover/load')->body(), true)['html']);
        $disk = json_decode($this->request('GET', '/dashboard/popover/disk')->body(), true)['html'];
        self::assertStringContainsString('Disk space: 50%', $disk);
        self::assertStringContainsString('Inodes used', $disk);
    }

    public function testTheServerDetailsAppearOnHoverOverTheHostnameNotAsAWidget(): void
    {
        $this->agent->succeed('system.info', ['system' => ['hostname' => 'srv1', 'kernel' => '6.8', 'timezone' => 'UTC', 'cpu' => ['cores' => 8, 'model' => 'EPYC'], 'uptime_seconds' => 90000], 'os' => ['pretty_name' => 'Ubuntu 24.04', 'supported' => true], 'agent_version' => '9.9.9']);
        $this->agent->succeed('agent.status', ['hostname' => 'srv1', 'os' => ['pretty_name' => 'Ubuntu 24.04'], 'load' => [0.1, 0.2, 0.3]]);
        // First, as the browser asks for it (JSON, before any page was drawn): a JSON request is not given the top
        // bar's data unless this route asks for it, and the restart button needs the host name from it.
        $res = json_decode($this->request('GET', '/dashboard/popover/server', [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest'])->body(), true);
        self::assertTrue($res['success']);
        self::assertStringContainsString('Ubuntu 24.04', $res['html']);
        self::assertStringContainsString('9.9.9', $res['html']);
        self::assertStringContainsString('data-bs-target="#nim-reboot"', $res['html'], 'the restart-the-server button needs the host name');
        self::assertStringContainsString('aria-label="Restart all services"', $res['html']);
        $page = $this->request('GET', '/')->body();
        self::assertStringNotContainsString('data-widget-host="server_info"', $page);
        self::assertStringNotContainsString('data-widget-host="services_live"', $page);
    }

    public function testTheTopBarHasTheVersionChangeLogAndFeedbackAndNoOperatingSystem(): void
    {
        $this->agent->succeed('agent.status', ['hostname' => 'srv1', 'os' => ['pretty_name' => 'Ubuntu 24.04'], 'load' => [0.1, 0.2, 0.3]]);
        $page = $this->request('GET', '/')->body();
        self::assertStringContainsString('data-bs-target="#nim-changelog"', $page);
        self::assertStringContainsString('id="nim-changelog"', $page);
        self::assertStringContainsString('data-bs-target="#nim-feedback"', $page);
        self::assertStringContainsString('id="nim-feedback"', $page);
        self::assertStringNotContainsString('<dt>OS</dt>', $page);
        $res = json_decode($this->request('GET', '/changelog/content')->body(), true);
        self::assertTrue($res['success']);
        self::assertStringContainsString('not installed', $res['html'], 'the test server has no CHANGELOG.md beside the panel');
    }

    public function testAnUnknownTileIsNotFound(): void
    {
        self::assertSame(404, $this->request('GET', '/dashboard/popover/nope')->status());
    }

    public function testTheSoftwareWidgetShowsEachPartWithItsActions(): void
    {
        $this->agent->succeed('components.status', [
            ['id' => 'web', 'name' => 'Web server', 'installed' => true, 'units' => ['nginx.service'], 'steps' => [['id' => 'running', 'status' => 'ok']]],
            ['id' => 'redis', 'name' => 'Redis', 'installed' => true, 'units' => ['redis-server.service'], 'steps' => [['id' => 'running', 'status' => 'todo']]],
            ['id' => 'mongodb', 'name' => 'MongoDB', 'installed' => false, 'units' => [], 'steps' => []],
        ]);
        $res = json_decode($this->request('GET', '/dashboard/widgets/software')->body(), true);
        self::assertTrue($res['success']);
        self::assertStringContainsString('data-software-op="restart" data-unit="nginx.service"', $res['html']);
        self::assertStringContainsString('data-software-op="start" data-unit="redis-server.service"', $res['html']);
        self::assertStringContainsString('Not installed', $res['html']);
    }
}
