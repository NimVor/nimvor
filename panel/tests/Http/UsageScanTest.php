<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Services\MonitoringService;
use NimVor\Tests\Support\HttpTestCase;

/**
 * The scheduler measures a website's disk use at most hourly. A website without a measured row (no folder on
 * disk, a measurement that failed) used to make it walk every website's files again on every 5-minute pass.
 */
final class UsageScanTest extends HttpTestCase
{
    private Connection $db;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->db = $this->app->container->get(Connection::class);
        foreach (['shop.test', 'ghost.test'] as $d) {
            $this->db->run("INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, ?, 'static', ?, ?, 'u1', NULL, 'active', 'x', 'x')", [$d, '/home/nimvor/' . $d, '/home/nimvor/' . $d . '/public_html']);
        }
        $this->agent->succeed('system.info', ['system' => ['cpu' => ['cores' => 1]]]);
        $this->agent->succeed('system.metrics', ['cpu_percent' => 1, 'memory' => ['used_percent' => 1], 'load' => ['load1' => 0], 'disks' => [], 'network' => []]);
        $this->agent->succeed('services.status', []);
        $this->agent->succeed('discovery.certificates', []);
        $this->agent->succeed('sites.bandwidth', []);
        // ghost.test has no folder on the server.
        $this->agent->succeed('sites.usage', [['domain' => 'shop.test', 'bytes' => 1048576, 'files' => 3], ['domain' => 'ghost.test', 'not_found' => true]]);
    }

    public function testAWebsiteWithoutAFolderIsNotMeasuredAgainEveryPass(): void
    {
        $mon = $this->app->container->get(MonitoringService::class);
        $mon->run();
        self::assertSame(1, $this->calls('sites.usage'));
        self::assertSame(['ghost.test', 'shop.test'], $this->sorted($this->lastCall('sites.usage')['domains']));
        self::assertSame(1, (int) $this->db->scalar('SELECT COUNT(*) FROM website_usage'), 'only the website with a folder has a measurement');

        // The next passes (every 5 minutes) do not walk any files again.
        $mon->run();
        $mon->run();
        self::assertSame(1, $this->calls('sites.usage'));
    }

    public function testAFailedMeasurementWaitsForTheHour(): void
    {
        $this->agent->fail('sites.usage', 504, 'timeout', 'The measurement took too long.');
        $mon = $this->app->container->get(MonitoringService::class);
        $mon->run();
        $mon->run();
        self::assertSame(1, $this->calls('sites.usage'), 'a failure is not retried on the next pass');
        self::assertSame(0, (int) $this->db->scalar('SELECT COUNT(*) FROM website_usage'));
    }

    public function testEachWebsiteIsMeasuredAgainAfterAnHour(): void
    {
        $mon = $this->app->container->get(MonitoringService::class);
        $mon->run();
        $this->db->run("UPDATE websites SET usage_checked_at = '2000-01-01T00:00:00+00:00' WHERE domain = 'shop.test'");
        $mon->run();
        self::assertSame(2, $this->calls('sites.usage'));
        self::assertSame(['shop.test'], $this->lastCall('sites.usage')['domains'], 'only the website that is due is walked');

        // A new website is measured on the next pass without walking the others.
        $this->db->run("INSERT INTO websites (server_id, domain, kind, root, document_root, system_user, owner_id, status, created_at, updated_at) VALUES (1, 'new.test', 'static', '/home/nimvor/new.test', '/home/nimvor/new.test/public_html', 'u1', NULL, 'active', 'x', 'x')");
        $mon->run();
        self::assertSame(['new.test'], $this->lastCall('sites.usage')['domains']);
    }

    /** @param list<string> $a @return list<string> */
    private function sorted(array $a): array
    {
        sort($a);
        return $a;
    }
}
