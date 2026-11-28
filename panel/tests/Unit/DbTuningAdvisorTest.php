<?php

declare(strict_types=1);

namespace NimVor\Tests\Unit;

use NimVor\Services\DbTuningAdvisor;
use PHPUnit\Framework\TestCase;

final class DbTuningAdvisorTest extends TestCase
{
    /** @param list<array{level: string, text: string}> $a */
    private static function texts(array $a, string $level): string
    {
        return implode("\n", array_map(static fn (array $x): string => $x['text'], array_filter($a, static fn (array $x): bool => $x['level'] === $level)));
    }

    public function testAHealthyBusyServerHasNothingToFix(): void
    {
        $a = DbTuningAdvisor::advise(
            ['Uptime' => '864000', 'Max_used_connections' => '40', 'Threads_running' => '2', 'Innodb_buffer_pool_read_requests' => '1000000', 'Innodb_buffer_pool_reads' => '500', 'Slow_queries' => '0', 'Created_tmp_tables' => '1000', 'Created_tmp_disk_tables' => '50'],
            ['max_connections' => '200', 'slow_query_log' => 'ON'],
        );
        self::assertSame('', self::texts($a, 'warn'));
        self::assertStringContainsString('at most 40 of 200', self::texts($a, 'ok'));
        self::assertStringContainsString('99.95%', self::texts($a, 'ok'));
    }

    public function testEachSymptomGetsItsOwnAdvice(): void
    {
        $a = DbTuningAdvisor::advise(
            ['Uptime' => '1800', 'Max_used_connections' => '190', 'Threads_running' => '25', 'Innodb_buffer_pool_read_requests' => '100000', 'Innodb_buffer_pool_reads' => '5000', 'Slow_queries' => '300', 'Created_tmp_tables' => '400', 'Created_tmp_disk_tables' => '300'],
            ['max_connections' => '200', 'slow_query_log' => 'OFF'],
        );
        $warn = self::texts($a, 'warn');
        foreach (['190 of 200', '5.0% of data reads', '25 statements', '300 slow queries', 'temporary tables spilled'] as $want) {
            self::assertStringContainsString($want, $warn, $want);
        }
        self::assertStringContainsString('Turn on the slow query log', $warn);
        self::assertStringContainsString('30 minutes', self::texts($a, 'info'), 'a young server says its counters are short');
    }

    public function testMissingCountersGiveNoNoise(): void
    {
        self::assertSame([], array_values(array_filter(DbTuningAdvisor::advise([], []), static fn (array $x): bool => $x['level'] === 'warn')));
    }
}
