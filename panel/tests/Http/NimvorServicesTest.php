<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Tests\Support\HttpTestCase;

/** NimVor's own services on the Services page. */
final class NimvorServicesTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('resell', 'reseller');
        $this->agent->succeed('services.status', [['unit' => 'nimvor-panel.service', 'description' => 'Panel', 'active_state' => 'active', 'sub_state' => 'running', 'unit_file_state' => 'enabled', 'main_pid' => 10, 'active_since' => null]]);
        $this->agent->succeed('nimvor.services', ['services' => [
            ['key' => 'agent', 'unit' => 'nimvor-agent.service', 'label' => 'Agent (privileged actions)', 'restartable' => true, 'status' => ['active_state' => 'active', 'sub_state' => 'running']],
        ]]);
        $this->agent->succeed('nimvor.restart', ['unit' => 'nimvor-agent.service', 'scheduled' => true]);
    }

    public function testRestartTheAgent(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/services')->body();
        self::assertStringContainsString('Agent (privileged actions)', $page);
        self::assertStringContainsString('action="/services/nimvor/agent/restart"', $page);
        self::assertStringNotContainsString('name="unit" value="nimvor-panel.service"', $page, 'the panel is not stopped or restarted from the table');
        $this->post('/services/nimvor/agent/restart');
        self::assertSame(['key' => 'agent'], $this->lastCall('nimvor.restart'));
        self::assertStringContainsString('restarts in a few seconds', (string) $this->flash('success'));
    }

    public function testOthersCannot(): void
    {
        $this->login('resell');
        $this->post('/services/nimvor/agent/restart');
        self::assertSame(0, $this->calls('nimvor.restart'));
    }
}
