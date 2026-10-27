<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Services\SettingsService;
use NimVor\Tests\Support\HttpTestCase;

/** Server → Nginx: status, test/reload and tuning, admin only. */
final class NginxManagerTest extends HttpTestCase
{
    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->seedUser('cust', 'customer');
        $this->agent->succeed('nginx.status', ['installed' => true, 'running' => true, 'config_ok' => true, 'config_error' => '', 'tuning' => true]);
    }

    public function testAdminSeesStatusAndAppliesTuning(): void
    {
        $this->login('admin');
        $page = $this->request('GET', '/nginx')->body();
        foreach (['Nginx', 'configuration valid', 'Tuning', 'Max upload size'] as $want) {
            self::assertStringContainsString($want, $page, $want);
        }

        $this->agent->succeed('nginx.tuning_apply', ['applied' => true]);
        $this->post('/nginx/tuning', [
            'client_max_body_mb' => '256', 'keepalive_timeout' => '75',
            'client_body_timeout' => '60', 'send_timeout' => '60', 'gzip' => '1', 'hide_version' => '1',
        ]);
        $call = $this->lastCall('nginx.tuning_apply');
        self::assertSame(256, $call['client_max_body_mb']);
        self::assertSame(75, $call['keepalive_timeout']);
        self::assertTrue($call['hide_version']);
        self::assertSame(256, $this->app->container->get(SettingsService::class)->int('nginx_client_max_body_mb'));

        $this->agent->succeed('nginx.test', ['ok' => true]);
        $this->post('/nginx/test');
        self::assertStringContainsString('valid', (string) $this->flash('success'));
    }

    public function testInvalidTuningRolledBackSurfacesError(): void
    {
        $this->login('admin');
        $this->agent->fail('nginx.tuning_apply', 500, 'failed', 'rolled back');
        $this->post('/nginx/tuning', ['client_max_body_mb' => '512', 'keepalive_timeout' => '65', 'client_body_timeout' => '60', 'send_timeout' => '60']);
        self::assertStringContainsString('rolled back', (string) $this->flash('error'));
    }

    public function testCustomersCannotReachNginx(): void
    {
        $this->login('cust');
        self::assertSame(403, $this->request('GET', '/nginx')->status());
        self::assertSame(403, $this->post('/nginx/tuning', ['client_max_body_mb' => '256'])->status());
        self::assertSame(0, $this->calls('nginx.tuning_apply'));
    }
}
