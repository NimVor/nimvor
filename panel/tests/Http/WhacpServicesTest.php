<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Tests\Support\HttpTestCase;

/** NimVor's own services on the Services page. */
final class WhacpServicesTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('resell', 'reseller');
        $this->agent->succeed('services.status', [['unit' => 'whacp-panel.service', 'description' => 'Panel', 'active_state' => 'active', 'sub_state' => 'running', 'unit_file_state' => 'enabled', 'main_pid' => 10, 'active_since' => null]]);
        $this->agent->succeed('whacp.services', ['services' => [
            ['key' => 'agent', 'unit' => 'whacp-agent.service', 'label' => 'Agent (privileged actions)', 'restartable' => true, 'status' => ['active_state' => 'active', 'sub_state' => 'running']],
        ]]);
        $this->agent->succeed('whacp.restart', ['unit' => 'whacp-agent.service', 'scheduled' => true]);
    }

    public function testRestartTheAgent(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/services')->body();
        self::assertStringContainsString('Agent (privileged actions)', $page);
        self::assertStringContainsString('action="/services/whacp/agent/restart"', $page);
        self::assertStringNotContainsString('name="unit" value="whacp-panel.service"', $page, 'the panel is not stopped or restarted from the table');
        $this->post('/services/whacp/agent/restart');
        self::assertSame(['key' => 'agent'], $this->lastCall('whacp.restart'));
        self::assertStringContainsString('restarts in a few seconds', (string) $this->flash('success'));
    }

    public function testOthersCannot(): void
    {
        $this->login('resell');
        $this->post('/services/whacp/agent/restart');
        self::assertSame(0, $this->calls('whacp.restart'));
    }
}
