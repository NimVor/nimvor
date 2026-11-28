<?php

declare(strict_types=1);

namespace NimVor\Services;

/**
 * Reads the database server's own counters and says in plain words how its
 * settings look for the workload it has seen.
 *
 * Two kinds of numbers are kept apart: cumulative counters (Max_used_connections,
 * Slow_queries, the InnoDB read counters, ...) count from the last start of the
 * server and reset when it restarts; sampled values (Threads_running,
 * Threads_connected) are a snapshot of this moment. Nothing is recommended from
 * cumulative counters until the server has run for a day.
 */
final class DbTuningAdvisor
{
    /** Seconds of uptime after which cumulative counters say something about normal traffic. */
    public const ENOUGH_UPTIME = 86400;

    /**
     * @param array<string, mixed> $status SHOW GLOBAL STATUS values (strings)
     * @param array<string, mixed> $vars   the settings in force now
     * @return array{uptime: int, uptime_text: string, enough: bool, cumulative: list<array{level: string, text: string}>, snapshot: list<array{level: string, text: string}>}
     */
    public static function advise(array $status, array $vars): array
    {
        $n = static fn (string $k): float => is_numeric($status[$k] ?? null) ? (float) $status[$k] : 0.0;
        $v = static fn (string $k): float => is_numeric($vars[$k] ?? null) ? (float) $vars[$k] : 0.0;
        $uptime = (int) $n('Uptime');
        $enough = $uptime >= self::ENOUGH_UPTIME;
        // Too early: a finding is a fact, not advice, and never asks for a change.
        $lvl = static fn (string $level) => $enough || $level === 'ok' ? $level : 'info';
        $cum = [];
        $now = [];

        $peak = $n('Max_used_connections');
        $max = $v('max_connections');
        if ($max > 0 && $peak > 0) {
            $hot = $peak >= 0.8 * $max;
            $cum[] = ['level' => $lvl($hot ? 'warn' : 'ok'), 'text' => 'Peak connections: ' . (int) $peak . ' of ' . (int) $max . ' allowed (Max_used_connections).'
                . ($hot && $enough ? ' The peak came close to the limit: raise the maximum connections, or look for applications that keep connections open.' : '')];
        }

        $requests = $n('Innodb_buffer_pool_read_requests');
        if ($requests >= 10000) {
            $miss = $n('Innodb_buffer_pool_reads') / $requests * 100;
            $cum[] = ['level' => $lvl($miss > 1 ? 'warn' : 'ok'), 'text' => sprintf('InnoDB buffer-pool read miss ratio: %.2f%% (Innodb_buffer_pool_reads ÷ Innodb_buffer_pool_read_requests). It is the share of reads the pool could not answer from memory, not how busy the disk is.', $miss)
                . ($miss > 1 && $enough ? ' If the server has memory to spare, a larger buffer pool lowers it.' : '')];
        }

        $slow = $n('Slow_queries');
        if ($slow > 0) {
            $log = strtoupper((string) ($vars['slow_query_log'] ?? '')) === 'ON' || ($vars['slow_query_log'] ?? '') === '1';
            $cum[] = ['level' => 'info', 'text' => number_format($slow) . ' slow queries since start (Slow_queries)'
                . ($log ? '; they are in the slow query log, where the slowest ones show what to index or rewrite.' : '. Turn on the slow query log to see which ones.')];
        }

        $tmp = $n('Created_tmp_tables');
        if ($tmp >= 100) {
            $disk = $n('Created_tmp_disk_tables') / $tmp * 100;
            $cum[] = ['level' => $lvl($disk > 25 ? 'warn' : 'ok'), 'text' => sprintf('%.0f%% of temporary tables were written to disk (Created_tmp_disk_tables ÷ Created_tmp_tables).', $disk)
                . ($disk > 25 && $enough ? ' Raising the in-memory temporary table and heap table sizes together can help.' : '')];
        }

        $running = (int) $n('Threads_running');
        $now[] = ['level' => $running >= 10 ? 'warn' : 'ok', 'text' => $running . ' statements running right now (Threads_running) of ' . (int) $n('Threads_connected') . ' open connections. This is a snapshot of this moment, not an average: reload the page a few times, or see the Connections tab.'];

        return ['uptime' => $uptime, 'uptime_text' => self::duration($uptime), 'enough' => $enough, 'cumulative' => $cum, 'snapshot' => $now];
    }

    public static function duration(int $seconds): string
    {
        if ($seconds < 120) {
            return $seconds . ' seconds';
        }
        if ($seconds < 7200) {
            return intdiv($seconds, 60) . ' minutes';
        }
        if ($seconds < 172800) {
            return intdiv($seconds, 3600) . ' hours';
        }
        return intdiv($seconds, 86400) . ' days ' . intdiv($seconds % 86400, 3600) . ' hours';
    }
}
