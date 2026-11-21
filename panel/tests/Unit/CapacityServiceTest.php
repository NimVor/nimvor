<?php

declare(strict_types=1);

namespace NimVor\Tests\Unit;

use NimVor\Services\CapacityService;
use PHPUnit\Framework\TestCase;

final class CapacityServiceTest extends TestCase
{
    /** @return list<array{0: float, 1: float}> */
    private static function days(int $n, callable $f): array
    {
        $out = [];
        for ($i = 0; $i < $n; $i++) {
            $out[] = [(float) $i, (float) $f($i)];
        }
        return $out;
    }

    public function testLineThroughExactPoints(): void
    {
        $l = CapacityService::line(self::days(10, static fn (int $i): float => 40 + 2 * $i));
        self::assertNotNull($l);
        self::assertEqualsWithDelta(2.0, $l['slope'], 1e-9);
        self::assertEqualsWithDelta(40.0, $l['intercept'], 1e-9);
        self::assertEqualsWithDelta(1.0, $l['fit'], 1e-9);
        self::assertNull(CapacityService::line([[1.0, 2.0]]));
    }

    public function testASteadyGrowthGivesDaysUntilFull(): void
    {
        // 50% now (day 9 = 68%), +2%/day: full in 16 days from the last point.
        $r = CapacityService::daysToLimit(self::days(10, static fn (int $i): float => 50 + 2 * $i), 100.0);
        self::assertSame('growing', $r['state']);
        self::assertSame(16, $r['days']);
        self::assertEqualsWithDelta(14.0, $r['per_week'], 0.01);
        self::assertFalse($r['beyond_horizon']);
    }

    public function testTooFewDaysIsNotForecast(): void
    {
        $r = CapacityService::daysToLimit(self::days(5, static fn (int $i): float => 50 + 2 * $i), 100.0);
        self::assertSame(['state' => 'insufficient', 'have' => 5, 'need' => 7], $r);
    }

    public function testNoiseHasNoTrendAndFlatIsStable(): void
    {
        $noisy = [30, 80, 20, 90, 25, 85, 40, 60, 10, 95];
        self::assertSame('no_trend', CapacityService::daysToLimit(self::days(10, static fn (int $i): float => $noisy[$i]), 100.0)['state']);
        self::assertSame('stable', CapacityService::daysToLimit(self::days(10, static fn (int $i): float => 42), 100.0)['state'], 'a constant series is stable');
    }

    public function testAFallingValueIsNeverForecastToFill(): void
    {
        $r = CapacityService::daysToLimit(self::days(10, static fn (int $i): float => 80 - $i), 100.0);
        self::assertSame('stable', $r['state']);
    }

    public function testSlowGrowthIsBeyondTheHorizon(): void
    {
        $r = CapacityService::daysToLimit(self::days(10, static fn (int $i): float => 10 + 0.05 * $i), 100.0);
        self::assertSame('growing', $r['state']);
        self::assertTrue($r['beyond_horizon']);
    }
}
