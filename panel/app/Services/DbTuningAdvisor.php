<?php

declare(strict_types=1);

namespace NimVor\Services;

/**
 * Reads the database server's own counters and says in plain words whether
 * its settings look right for the workload it has seen. The counters count
 * from the last start of the server, so a young server proves little: the
 * first line says how much time they cover.
 */
final class DbTuningAdvisor
{
    /**
     * @param array<string, mixed> $status SHOW GLOBAL STATUS values (strings)
     * @param array<string, mixed> $vars   the settings in force now
     * @return list<array{level: string, text: string}> level: ok, info or warn
     */
    public static function advise(array $status, array $vars): array
    {
        $n = static fn (string $k): float => is_numeric($status[$k] ?? null) ? (float) $status[$k] : 0.0;
        $v = static fn (string $k): float => is_numeric($vars[$k] ?? null) ? (float) $vars[$k] : 0.0;
        $out = [];

        $hours = $n('Uptime') / 3600;
        if ($hours < 24) {
            $out[] = ['level' => 'info', 'text' => 'These counters cover only ' . ($hours < 1 ? 'the last ' . max(1, (int) round($n('Uptime') / 60)) . ' minutes' : 'the last ' . (int) round($hours) . ' hours') . ' since the server started; check again after a normal working day before changing anything.'];
        }

        $peak = $n('Max_used_connections');
        $max = $v('max_connections');
        if ($max > 0) {
            $out[] = $peak >= 0.8 * $max
                ? ['level' => 'warn', 'text' => 'At its busiest the server had ' . (int) $peak . ' of ' . (int) $max . ' connections open. Raise the maximum connections, or look for applications that keep connections open.']
                : ['level' => 'ok', 'text' => 'Connections are fine: at most ' . (int) $peak . ' of ' . (int) $max . ' were open at once.'];
        }

        $requests = $n('Innodb_buffer_pool_read_requests');
        if ($requests >= 10000) {
            $miss = $n('Innodb_buffer_pool_reads') / $requests * 100;
            $out[] = $miss > 1
                ? ['level' => 'warn', 'text' => sprintf('%.1f%% of data reads had to go to the disk. If the server has memory to spare, a larger InnoDB buffer pool will help.', $miss)]
                : ['level' => 'ok', 'text' => sprintf('The InnoDB buffer pool serves %.2f%% of reads from memory.', 100 - $miss)];
        }

        $running = $n('Threads_running');
        $out[] = $running >= 10
            ? ['level' => 'warn', 'text' => (int) $running . ' statements are running at this moment. Look at the Connections tab for what they are.']
            : ['level' => 'ok', 'text' => (int) $running . ' statements running at this moment (a snapshot, not an average).'];

        $slow = $n('Slow_queries');
        if ($slow > 0) {
            $log = strtoupper((string) ($vars['slow_query_log'] ?? '')) === 'ON' || ($vars['slow_query_log'] ?? '') === '1';
            $out[] = ['level' => $slow >= 100 ? 'warn' : 'info', 'text' => number_format($slow) . ' slow queries so far' . ($log ? '; they are in the slow query log, where the slowest ones show what to index or rewrite.' : '. Turn on the slow query log to see which ones.')];
        }

        $tmp = $n('Created_tmp_tables');
        if ($tmp >= 100 && $n('Created_tmp_disk_tables') / $tmp > 0.25) {
            $out[] = ['level' => 'warn', 'text' => sprintf('%.0f%% of temporary tables spilled to the disk. Raising the in-memory temporary table and heap table sizes together can help.', $n('Created_tmp_disk_tables') / $tmp * 100)];
        }

        return $out;
    }
}
