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
}
