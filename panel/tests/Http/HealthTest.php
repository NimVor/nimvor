<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Services\HealthService;
use NimVor\Tests\Support\HttpTestCase;

/** Server → Health: every check in one place. */
final class HealthTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->agent->succeed('system.info', ['system' => ['hostname' => 'web1', 'cpu' => ['cores' => 2]]]);
        $this->agent->succeed('system.metrics', [
            'disks' => [['mount_point' => '/', 'used_percent' => 97.0, 'free_bytes' => 1073741824]],
            'memory' => ['used_percent' => 40.0, 'total_bytes' => 4294967296, 'swap_total_bytes' => 0],
            'load' => ['load1' => 9.0],
        ]);
        $this->agent->succeed('components.status', [
            ['id' => 'web', 'name' => 'Web server', 'installed' => true, 'steps' => [['id' => 'running', 'status' => 'todo', 'detail' => 'Not running: nginx.service']]],
            ['id' => 'redis', 'name' => 'Redis', 'installed' => true, 'steps' => [['id' => 'boot', 'status' => 'todo', 'detail' => 'Not started at boot']]],
            ['id' => 'dns', 'name' => 'DNS', 'installed' => false, 'steps' => []],
        ]);
        $this->agent->succeed('security.audit', [
            ['id' => 'firewall', 'title' => 'Firewall', 'status' => 'fail', 'severity' => 'high', 'detail' => 'off'],
            ['id' => 'ssh_root', 'title' => 'SSH root login', 'status' => 'warn', 'severity' => 'medium', 'detail' => 'allowed'],
            ['id' => 'fail2ban', 'title' => 'Fail2ban', 'status' => 'pass', 'severity' => 'medium', 'detail' => 'on'],
        ]);
        $this->agent->succeed('packages.updates', ['count' => 3, 'security' => 1, 'index_stale' => true, 'index_refreshed' => '2026-09-01T00:00:00Z']);
        $this->agent->succeed('services.status', []);
        $this->agent->succeed('packages.list', []);
    }

    public function testEverySectionWithTheWorstFirstAndLinks(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/server/health')->body();
        foreach (array_keys(HealthService::SECTIONS) as $s) {
            self::assertStringContainsString('data-section="' . $s . '"', $page);
        }
        self::assertStringContainsString('data-section="resources" data-status="fail"', $page, 'disk 97% full');
        self::assertStringContainsString('2 core(s)', $page);
        self::assertStringContainsString('data-section="components" data-status="fail"', $page, 'nginx not running');
        self::assertStringContainsString('Not started at boot', $page);
        self::assertStringNotContainsString('>DNS<span class="visually-hidden">', $page, 'a component not installed nor expected is not listed');
        self::assertStringContainsString('data-section="security" data-status="fail"', $page);
        self::assertStringContainsString('1 recommendation(s)', $page);
        self::assertStringContainsString('3 update(s), 1 of them security', $page);
        self::assertStringContainsString('Not refreshed for a while', $page);
        self::assertStringContainsString('href="/security"', $page);
        self::assertMatchesRegularExpression('/data-health="fail"/', $page);
    }

    public function testAgentDownIsShownNotFatal(): void
    {
        $this->agent->down = true;
        $this->login('admin');
        $res = $this->request('GET', '/server/health');
        self::assertSame(200, $res->status());
        self::assertStringContainsString('data-check-status="unknown"', $res->body());
    }

    public function testCustomersCannotSeeIt(): void
    {
        $this->login('cust');
        self::assertNotSame(200, $this->request('GET', '/server/health')->status());
    }
}
