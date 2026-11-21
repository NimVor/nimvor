<?php

declare(strict_types=1);

namespace NimVor\Tests\Http;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Services\WebsiteService;
use NimVor\Tests\Support\HttpTestCase;

/** The Capacity forecast and Traffic dashboard widgets. */
final class CapacityWidgetsTest extends HttpTestCase
{
    private Connection $conn;

    protected function setUp(): void
    {
        parent::setUp();
        $this->seedUser('admin', 'admin');
        $this->conn = $this->app->container->get(Connection::class);
    }

    /** @return array<string, mixed> */
    private function widget(string $id): array
    {
        $res = $this->request('GET', '/dashboard/widgets/' . $id, [], ['accept' => 'application/json', 'x-requested-with' => 'XMLHttpRequest']);
        self::assertSame(200, $res->status(), $res->body());
        return (array) json_decode($res->body(), true);
    }

    /** One sample every 5 minutes for $days days, ending now; disk grows by $diskPerDay a day. */
    private function samples(int $days, float $diskStart, float $diskPerDay): void
    {
        $now = time();
        $this->conn->run('DELETE FROM metric_samples');
        $this->conn->run('DELETE FROM widget_cache');
        $this->conn->transaction(function () use ($days, $diskStart, $diskPerDay, $now): void {
            for ($t = $now - $days * 86400; $t <= $now; $t += 300) {
                $day = ($t - ($now - $days * 86400)) / 86400;
                $this->conn->run('INSERT INTO metric_samples (server_id, taken_at, cpu, mem, disk, load1, rx_bps, tx_bps) VALUES (1, ?, 20, 50, ?, 0.5, 1000, 4000)', [$t, $diskStart + $diskPerDay * $day]);
            }
        });
    }

    public function testNoMeasurementsIsEmptyNotAForecast(): void
    {
        $this->login('admin');
        self::assertSame('empty', $this->widget('capacity_forecast')['state']);
    }

    public function testAWeekOfSteadyGrowthGivesADate(): void
    {
        $this->samples(10, 50.0, 2.0);
        $this->login('admin');
        $w = $this->widget('capacity_forecast');
        self::assertSame('ok', $w['state']);
        self::assertStringContainsString('Growing by 14.0% a week', $w['html']);
        self::assertMatchesRegularExpression('/the fullest disk is full in about \d+ days/', $w['html']);
        self::assertStringContainsString('Not growing', $w['html'], 'memory is flat');
    }

    public function testTooLittleHistorySaysSoInsteadOfGuessing(): void
    {
        $this->samples(3, 50.0, 2.0);
        $this->login('admin');
        $html = $this->widget('capacity_forecast')['html'];
        self::assertStringContainsString('Not enough data yet', $html);
        self::assertStringNotContainsString('full in about', $html);
    }

    public function testTrafficEstimateAndTopWebsites(): void
    {
        $this->samples(1, 10.0, 0.0);
        $admin = (array) $this->app->container->get(UserRepository::class)->find(1);
        $sites = $this->app->container->get(WebsiteService::class);
        $a = $sites->create(['domain' => 'big.test', 'kind' => 'static'], $admin);
        $b = $sites->create(['domain' => 'small.test', 'kind' => 'static'], $admin);
        $month = gmdate('Y-m');
        foreach ([[$a, 5 * 1073741824], [$b, 1048576]] as [$id, $bytes]) {
            $this->conn->run('INSERT INTO website_usage (website_id, bytes, files, measured_at, bw_month, bw_bytes) VALUES (?, 1, 1, ?, ?, ?) ON CONFLICT (website_id) DO UPDATE SET bw_month = excluded.bw_month, bw_bytes = excluded.bw_bytes', [$id, gmdate('c'), $month, $bytes]);
        }
        $this->login('admin');
        $html = $this->widget('traffic_overview')['html'];
        self::assertStringContainsString('big.test', $html);
        self::assertStringContainsString('5 GB', $html);
        self::assertLessThan(strpos($html, 'small.test'), strpos($html, 'big.test'), 'the biggest first');
        self::assertStringContainsString('Estimated from the five-minute samples', $html);
        self::assertStringContainsString('Requests per second and visitors are not collected', $html);
    }

    public function testNothingMeasuredIsEmpty(): void
    {
        $this->login('admin');
        self::assertSame('empty', $this->widget('traffic_overview')['state']);
    }
}
