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

    private function tuningForm(array $over = []): array
    {
        return $over + ['client_max_body_mb' => '128', 'keepalive_timeout' => '65', 'client_body_timeout' => '60', 'send_timeout' => '60', 'gzip' => '1', 'hide_version' => '1'];
    }

    public function testBrotliIsOnByDefaultAndSentToTheAgent(): void
    {
        $this->login('admin');
        // On by default; without the module the page says it is not active yet and where to get it.
        $page = $this->request('GET', '/nginx')->body();
        self::assertMatchesRegularExpression('/id="ng-brotli"[^>]*checked/', $page);
        self::assertStringContainsString('not active yet', $page);
        self::assertStringContainsString('not installed', $page);

        $this->agent->succeed('nginx.tuning_apply', ['applied' => true, 'brotli_active' => false]);
        $this->post('/nginx/tuning', $this->tuningForm(['brotli' => '1']));
        self::assertTrue($this->lastCall('nginx.tuning_apply')['brotli']);
        self::assertStringContainsString('not active yet', (string) $this->flash('success'));
        $this->agent->succeed('nginx.tuning_apply', ['applied' => true, 'brotli_active' => true]);
        $this->post('/nginx/tuning', $this->tuningForm(['brotli' => '1']));
        self::assertSame('Nginx tuning applied.', $this->flash('success'));
        $this->post('/nginx/tuning', $this->tuningForm(['brotli' => '0']));
        self::assertFalse($this->lastCall('nginx.tuning_apply')['brotli']);
        self::assertFalse($this->app->container->get(SettingsService::class)->bool('nginx_brotli'));
    }

    public function testThePageSaysWhenBrotliIsActiveOrNeedsApply(): void
    {
        $this->login('admin');
        $mk = fn (string $state, bool $inConfig) => $this->agent->succeed('nginx.status', ['installed' => true, 'running' => true, 'config_ok' => true, 'config_error' => '', 'tuning' => true, 'brotli' => ['state' => $state, 'reason' => '', 'in_config' => $inConfig]]);
        $mk('functional', true);
        self::assertStringContainsString('Active.', $this->request('GET', '/nginx')->body());
        $mk('functional', false);
        self::assertStringContainsString('Press Apply to start using Brotli', $this->request('GET', '/nginx')->body());
        // The module disappeared while Brotli is in the configuration: a warning, and turning it off stays possible.
        $mk('missing', true);
        $page = $this->request('GET', '/nginx')->body();
        self::assertStringContainsString('refuse to start or reload', $page);
        self::assertDoesNotMatchRegularExpression('/id="ng-brotli"[^>]*disabled/', $page);
    }

    public function testARejectedTuningChangeDoesNotChangeTheStoredSettings(): void
    {
        $this->login('admin');
        $settings = $this->app->container->get(SettingsService::class);
        $before = $settings->int('nginx_client_max_body_mb');
        $this->agent->fail('nginx.tuning_apply', 500, 'failed', 'rolled back');
        $this->post('/nginx/tuning', $this->tuningForm(['client_max_body_mb' => '999', 'brotli' => '0']));
        self::assertSame($before, $settings->int('nginx_client_max_body_mb'));
        self::assertTrue($settings->bool('nginx_brotli'), 'the page must keep showing what is really on the server');
    }

    public function testCustomersCannotReachNginx(): void
    {
        $this->login('cust');
        self::assertSame(403, $this->request('GET', '/nginx')->status());
        self::assertSame(403, $this->post('/nginx/tuning', ['client_max_body_mb' => '256'])->status());
        self::assertSame(0, $this->calls('nginx.tuning_apply'));
    }
}
