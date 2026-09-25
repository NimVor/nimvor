<?php

declare(strict_types=1);

namespace WhaCP\Tests\Http;

use WhaCP\Repositories\SettingRepository;
use WhaCP\Services\Doctor;
use WhaCP\Tests\Support\HttpTestCase;

final class DoctorTest extends HttpTestCase
{
    /** @return array<string, array{status: string, check: string, detail: string}> */
    private function checks(): array
    {
        return array_column($this->app->container->get(Doctor::class)->run(), null, 'check');
    }

    public function testReportsProblems(): void
    {
        $this->agent->responses['/v1/health'] = new \WhaCP\Agent\AgentHttpResponse(200, (string) json_encode(['success' => true, 'data' => ['version' => '0.1.0'], 'request_id' => 't']));
        chmod($this->dir . '/token', 0o644);
        $r = $this->checks();
        self::assertSame(Doctor::FAIL, $r['Agent token']['status'], 'a world-readable token is a failure');
        self::assertStringContainsString('chmod o-rwx', $r['Agent token']['detail']);
        self::assertSame(Doctor::OK, $r['Database schema']['status']);
        self::assertSame(Doctor::OK, $r['Database integrity']['status']);
        self::assertSame(Doctor::WARN, $r['Scheduler']['status']);
        self::assertSame(Doctor::WARN, $r['Agent']['status'], 'version mismatch');

        chmod($this->dir . '/token', 0o600);
        $this->app->container->get(SettingRepository::class)->set(Doctor::SCHEDULER_KEY, gmdate('c'), null);
        $r = $this->checks();
        self::assertSame(Doctor::OK, $r['Agent token']['status']);
        self::assertSame(Doctor::OK, $r['Scheduler']['status']);
    }

    public function testMissingKey(): void
    {
        $this->agent->responses['/v1/health'] = new \WhaCP\Agent\AgentHttpResponse(200, (string) json_encode(['success' => true, 'data' => ['version' => '0.5.0'], 'request_id' => 't']));
        $cfg = $this->app->container->get(\WhaCP\Core\Config::class);
        $key = (string) $cfg->get('security.key_file');
        if (is_file($key)) {
            self::markTestSkipped('the configured key exists');
        }
        self::assertSame(Doctor::WARN, $this->checks()['Panel encryption key']['status'], 'not created yet');
        $this->app->container->get(\WhaCP\Database\Connection::class)->run("INSERT INTO servers (name, kind, url, token_enc, created_at) VALUES ('edge', 'remote', 'https://192.0.2.1:8443', 'v1:x', '2026-01-01')");
        self::assertSame(Doctor::FAIL, $this->checks()['Panel encryption key']['status'], 'secrets stored without their key');
    }

    public function testPermissionRules(): void
    {
        $f = $this->dir . '/secret';
        touch($f);
        chmod($f, 0o640);
        self::assertSame(Doctor::OK, Doctor::permissions($f, false)[0]);
        chmod($f, 0o660);
        self::assertSame(Doctor::WARN, Doctor::permissions($f, false)[0]);
        chmod($f, 0o604);
        self::assertSame(Doctor::FAIL, Doctor::permissions($f, false)[0]);
        self::assertSame(Doctor::FAIL, Doctor::permissions($this->dir . '/missing', false)[0]);
    }

    public function testServerChecks(): void
    {
        $this->agent->responses['/v1/health'] = new \WhaCP\Agent\AgentHttpResponse(200, (string) json_encode(['success' => true, 'data' => ['version' => '1.0.0-rc1'], 'request_id' => 't']));
        $this->agent->succeed('system.metrics', ['disks' => [
            ['mount_point' => '/', 'used_percent' => 42.0, 'free_bytes' => 50 * 1024 ** 3],
            ['mount_point' => '/var', 'used_percent' => 88.5, 'free_bytes' => 2 * 1024 ** 3],
            ['mount_point' => '/home', 'used_percent' => 97.0, 'free_bytes' => 1024 ** 2],
        ]]);
        $this->agent->succeed('services.status', [
            ['unit' => 'nginx.service', 'load_state' => 'loaded', 'running' => true, 'unit_file_state' => 'enabled'],
            ['unit' => 'php8.3-fpm.service', 'load_state' => 'loaded', 'running' => false, 'unit_file_state' => 'enabled'],
            ['unit' => 'mariadb.service', 'load_state' => 'loaded', 'running' => true, 'unit_file_state' => 'enabled'],
        ]);
        $this->agent->succeed('packages.list', [['name' => 'certbot', 'installed' => false]]);
        $r = $this->checks();
        self::assertSame(Doctor::OK, $r['Disk /']['status']);
        self::assertSame(Doctor::WARN, $r['Disk /var']['status']);
        self::assertSame(Doctor::FAIL, $r['Disk /home']['status']);
        self::assertSame(Doctor::OK, $r['Nginx']['status']);
        self::assertSame(Doctor::FAIL, $r['PHP-FPM']['status'], 'an enabled FPM that is not running');
        self::assertStringContainsString('php8.3-fpm.service', $r['PHP-FPM']['detail']);
        self::assertSame(Doctor::OK, $r['Database server']['status']);
        self::assertArrayNotHasKey('Redis', $r, 'absent optional services are not reported');
        self::assertSame(Doctor::WARN, $r["Let's Encrypt (certbot)"]['status']);
        self::assertSame(Doctor::WARN, $r['Backups']['status'], 'no schedule');
    }
}
