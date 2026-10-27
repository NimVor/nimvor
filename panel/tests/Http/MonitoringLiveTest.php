<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Tests\Support\HttpTestCase;

/** Live statistics: server administrators only; the page polls JSON. */
final class MonitoringLiveTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('resell', 'reseller');
        $this->agent->succeed('system.live', ['timestamp' => '2026-09-28T10:00:00Z', 'cpu' => ['percent' => 12.5, 'threads' => 4, 'per_core' => []], 'memory' => ['total_bytes' => 1024], 'top_cpu' => [['pid' => 1, 'name' => 'nginx', 'user' => 'www-data']]]);
    }

    public function testAdministratorsSeeLiveStatistics(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/monitoring/live')->body();
        self::assertStringContainsString('data-wcp-live="/monitoring/live.json"', $page);
        self::assertStringContainsString('href="/monitoring/live"', $this->request('GET', '/monitoring')->body(), 'tab on the monitoring page');
        $json = json_decode($this->request('GET', '/monitoring/live.json', [], ['accept' => 'application/json'])->body(), true);
        self::assertSame(12.5, $json['cpu']['percent']);
        self::assertSame('nginx', $json['top_cpu'][0]['name']);
    }

    public function testOthersDoNot(): void
    {
        $this->login('resell');
        self::assertNotSame(200, $this->request('GET', '/monitoring/live')->status());
        self::assertNotSame(200, $this->request('GET', '/monitoring/live.json', [], ['accept' => 'application/json'])->status());
        self::assertSame(0, $this->calls('system.live'));
    }
}
