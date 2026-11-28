<?php

declare(strict_types=1);

namespace NimVor\Tests\Unit;

use NimVor\Services\DbTuningAdvisor;
use PHPUnit\Framework\TestCase;

final class DbTuningAdvisorTest extends TestCase
{
    /** @param list<array{level: string, text: string}> $items */
    private static function texts(array $items, string $level = ''): string
    {
        return implode("\n", array_map(static fn (array $x): string => $x['text'], array_filter($items, static fn (array $x): bool => $level === '' || $x['level'] === $level)));
    }

    public function testAHealthyServerAfterADayHasNothingToFix(): void
    {
        $a = DbTuningAdvisor::advise(
            ['Uptime' => '864000', 'Max_used_connections' => '40', 'Threads_running' => '2', 'Threads_connected' => '12', 'Innodb_buffer_pool_read_requests' => '1000000', 'Innodb_buffer_pool_reads' => '500', 'Slow_queries' => '0', 'Created_tmp_tables' => '1000', 'Created_tmp_disk_tables' => '50'],
            ['max_connections' => '200', 'slow_query_log' => 'ON'],
        );
        self::assertTrue($a['enough']);
        self::assertSame('10 days 0 hours', $a['uptime_text']);
        self::assertSame('', self::texts($a['cumulative'], 'warn') . self::texts($a['snapshot'], 'warn'));
        self::assertStringContainsString('Peak connections: 40 of 200', self::texts($a['cumulative']));
        self::assertStringContainsString('read miss ratio: 0.05%', self::texts($a['cumulative']));
    }

    public function testTheMissRatioIsNotCalledDiskUtilisation(): void
    {
        $text = self::texts(DbTuningAdvisor::advise(['Uptime' => '900000', 'Innodb_buffer_pool_read_requests' => '100000', 'Innodb_buffer_pool_reads' => '5000'], [])['cumulative']);
        self::assertStringContainsString('buffer-pool read miss ratio: 5.00%', $text);
        self::assertStringContainsString('Innodb_buffer_pool_reads ÷ Innodb_buffer_pool_read_requests', $text);
        self::assertStringContainsString('not how busy the disk is', $text);
    }

    public function testCumulativeCountersAreSeparatedFromTheSnapshot(): void
    {
        $a = DbTuningAdvisor::advise(['Uptime' => '900000', 'Threads_running' => '25', 'Threads_connected' => '90'], ['max_connections' => '200']);
        self::assertStringContainsString('Threads_running', self::texts($a['snapshot']));
        self::assertStringContainsString('snapshot of this moment, not an average', self::texts($a['snapshot']));
        self::assertStringNotContainsString('Threads_running', self::texts($a['cumulative']));
        self::assertSame('warn', $a['snapshot'][0]['level']);
    }

    public function testNothingIsRecommendedFromAYoungServer(): void
    {
        $a = DbTuningAdvisor::advise(
            ['Uptime' => '1800', 'Max_used_connections' => '190', 'Innodb_buffer_pool_read_requests' => '100000', 'Innodb_buffer_pool_reads' => '5000', 'Slow_queries' => '300', 'Created_tmp_tables' => '400', 'Created_tmp_disk_tables' => '300'],
            ['max_connections' => '200', 'slow_query_log' => 'OFF'],
        );
        self::assertFalse($a['enough']);
        self::assertSame('30 minutes', $a['uptime_text']);
        self::assertSame('', self::texts($a['cumulative'], 'warn'), 'no warnings from a short run');
        $all = self::texts($a['cumulative']);
        foreach (['Raise', 'raise', 'larger buffer pool', 'Raising'] as $advice) {
            self::assertStringNotContainsString($advice, $all, 'no advice from a short run');
        }
        self::assertStringContainsString('190 of 200', $all, 'the facts are still shown');
    }

    public function testAdviceComesAfterADay(): void
    {
        $a = DbTuningAdvisor::advise(
            ['Uptime' => '90000', 'Max_used_connections' => '190', 'Innodb_buffer_pool_read_requests' => '100000', 'Innodb_buffer_pool_reads' => '5000', 'Created_tmp_tables' => '400', 'Created_tmp_disk_tables' => '300'],
            ['max_connections' => '200'],
        );
        $warn = self::texts($a['cumulative'], 'warn');
        foreach (['190 of 200', 'larger buffer pool', 'temporary tables'] as $want) {
            self::assertStringContainsString($want, $warn, $want);
        }
    }

    public function testMissingCountersGiveNoNoise(): void
    {
        $a = DbTuningAdvisor::advise([], []);
        self::assertSame([], $a['cumulative']);
        self::assertFalse($a['enough']);
    }
}
