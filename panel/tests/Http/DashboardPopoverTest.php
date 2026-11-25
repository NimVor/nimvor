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
