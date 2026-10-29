<?php

declare(strict_types=1);

namespace NimVorSite\Tests\Http;

use NimVorSite\Core\Request;
use NimVorSite\Tests\Support\SiteTestCase;

final class InstallStatsTest extends SiteTestCase
{
    /** @param array<string, mixed>|string $body */
    private function report(array|string $body, string $type = 'application/json'): \NimVorSite\Core\Response
    {
        $raw = is_array($body) ? (string) json_encode($body) : $body;
        $req = new Request('POST', '/api/v1/installs', [], [], [], ['REMOTE_ADDR' => $this->ip, 'CONTENT_TYPE' => $type], $raw);
        return $this->app->handle($req);
    }

    /** @return array<string, string> */
    private function good(string $id = 'a1b2c3d4e5f60718293a4b5c6d7e8f90', array $over = []): array
    {
        return $over + ['id' => $id, 'version' => '1.0.0', 'os' => 'ubuntu', 'os_version' => '24.04', 'arch' => 'amd64'];
    }

    public function testAReportIsStoredWithoutASessionOrAToken(): void
    {
        $res = $this->report($this->good());
        self::assertSame(204, $res->status);
        self::assertSame([], $res->cookies(), 'no cookie for a server');
        $row = $this->row('SELECT * FROM installs');
        self::assertSame(['1.0.0', 'ubuntu', '24.04', 'amd64', '1'], [$row['version'], $row['os'], $row['os_version'], $row['arch'], (string) $row['pings']]);
        $columns = array_column($this->app->db->all('SHOW COLUMNS FROM installs'), 'Field');
        self::assertSame(['install_id', 'first_seen', 'last_seen', 'version', 'os', 'os_version', 'arch', 'pings'], $columns, 'no address, name or other identifier is stored');
    }

    public function testTheSameServerIsCountedOnceAndItsVersionIsUpdated(): void
    {
        $this->report($this->good());
        $this->report($this->good('a1b2c3d4e5f60718293a4b5c6d7e8f90', ['version' => '1.1.0']));
        $this->report($this->good('ffffffffffffffffffffffffffffffff'));
        self::assertSame(2, $this->rows('installs'));
        $row = $this->row("SELECT version, pings FROM installs WHERE install_id = 'a1b2c3d4e5f60718293a4b5c6d7e8f90'");
        self::assertSame(['1.1.0', 2], [$row['version'], (int) $row['pings']]);
    }

    public function testOnlyWellFormedReportsAreAccepted(): void
    {
        foreach ([
            $this->good('short'),
            $this->good('A1B2C3D4E5F60718293A4B5C6D7E8F90'),
            $this->good(over: ['version' => '1.0.0; DROP TABLE']),
            $this->good(over: ['os_version' => '24.04.1.1']),
            $this->good(over: ['version' => '']),
            ['id' => 5],
        ] as $bad) {
            self::assertSame(422, $this->report($bad)->status, json_encode($bad));
        }
        self::assertSame(400, $this->report('not json')->status);
        self::assertSame(400, $this->report((string) json_encode($this->good()), 'text/plain')->status);
        self::assertSame(400, $this->report((string) json_encode($this->good() + ['x' => str_repeat('a', 5000)]))->status);
        self::assertSame(0, $this->rows('installs'));
        $this->report($this->good(over: ['os' => '<script>', 'arch' => 'mips']));
        $row = $this->row('SELECT os, arch FROM installs');
        self::assertSame(['other', 'other'], [$row['os'], $row['arch']], 'unknown systems are filed as other');
    }

    public function testGetIsNotAllowedAndReportsAreRateLimited(): void
    {
        self::assertSame(405, $this->get('/api/v1/installs')->status);
        for ($i = 0; $i < 30; $i++) {
            self::assertSame(204, $this->report($this->good(sprintf('%032x', $i + 1)))->status);
        }
        self::assertSame(429, $this->report($this->good(sprintf('%032x', 99)))->status);
        self::assertSame(30, $this->rows('installs'));
    }

    public function testThePublicNumbersAndTheHomeStrip(): void
    {
        self::assertStringNotContainsString('have installed NimVor', $this->get('/')->body, 'nothing to boast about yet');
        self::assertStringContainsString('No reports yet', $this->get('/resources/stats')->body);
        $this->report($this->good());
        $this->report($this->good(sprintf('%032x', 2), ['os' => 'almalinux', 'os_version' => '9', 'arch' => 'arm64', 'version' => '1.0.1']));
        $home = $this->get('/')->body;
        self::assertStringContainsString('<strong>2</strong><span>servers have installed NimVor</span>', $home);
        $page = $this->get('/resources/stats')->body;
        self::assertStringContainsString('<strong>2</strong>', $page);
        self::assertStringContainsString('ubuntu 24.04', $page);
        self::assertStringContainsString('almalinux 9', $page);
        self::assertStringContainsString('arm64', $page);
        self::assertStringContainsString('1.0.1', $page);
        self::assertStringContainsString('part of installing NimVor', $page, 'the page says what the numbers are made from');
        self::assertStringNotContainsString('no-telemetry', $page);
        self::assertStringContainsString('<svg class="chart"', $page);
    }

    public function testThePrivacyPolicyDescribesTheReport(): void
    {
        $b = $this->get('/support/privacy')->body;
        self::assertStringContainsString('Installation reports', $b);
        self::assertStringContainsString('part of installing and upgrading NimVor', $b);
        self::assertStringContainsString('random identifier', $b);
    }
}
