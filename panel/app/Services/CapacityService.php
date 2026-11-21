<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Database\Connection;

/**
 * Trends and forecasts from the panel's own measurements (metric_samples, kept
 * 30 days; bandwidth per website from website_usage). A forecast needs at least
 * MIN_DAYS days with data and a clear, steady trend; otherwise the widget says
 * "not enough data" or "no clear trend" instead of inventing a date.
 */
final class CapacityService
{
    /** Days with data needed before anything is forecast. */
    public const MIN_DAYS = 7;
    /** How well a straight line must fit (R squared) to be trusted. */
    public const MIN_FIT = 0.6;
    /** A forecast beyond this many days is reported as "not within a year". */
    public const HORIZON = 365;

    public function __construct(
        private readonly Connection $db,
        private readonly ServerContext $server,
    ) {
    }

    /**
     * Least squares line through (x, y) points.
     *
     * @param list<array{0: float, 1: float}> $pts
     * @return array{slope: float, intercept: float, fit: float}|null null with fewer than two points
     */
    public static function line(array $pts): ?array
    {
        $n = count($pts);
        if ($n < 2) {
            return null;
        }
        $sx = $sy = $sxx = $sxy = $syy = 0.0;
        foreach ($pts as [$x, $y]) {
            $sx += $x;
            $sy += $y;
            $sxx += $x * $x;
            $sxy += $x * $y;
            $syy += $y * $y;
        }
        $den = $n * $sxx - $sx * $sx;
        if ($den == 0.0) {
            return null;
        }
        $slope = ($n * $sxy - $sx * $sy) / $den;
        $intercept = ($sy - $slope * $sx) / $n;
        $varY = $n * $syy - $sy * $sy;
        $fit = $varY <= 0 ? 0.0 : (($n * $sxy - $sx * $sy) ** 2) / ($den * $varY);
        return ['slope' => $slope, 'intercept' => $intercept, 'fit' => $fit];
    }

    /**
     * Days until a value that follows a daily series reaches a limit.
     *
     * @param list<array{0: float, 1: float}> $daily (day number, value)
     * @return array<string, mixed> state: insufficient, stable, no_trend, growing (days, beyond_horizon)
     */
    public static function daysToLimit(array $daily, float $limit): array
    {
        if (count($daily) < self::MIN_DAYS) {
            return ['state' => 'insufficient', 'have' => count($daily), 'need' => self::MIN_DAYS];
        }
        $l = self::line($daily);
        if ($l === null) {
            return ['state' => 'insufficient', 'have' => count($daily), 'need' => self::MIN_DAYS];
        }
        $last = $daily[count($daily) - 1];
        $now = $l['intercept'] + $l['slope'] * $last[0];
        $perWeek = round($l['slope'] * 7, 2);
        if ($l['slope'] <= 0.01) {
            return ['state' => 'stable', 'per_week' => $perWeek];
        }
        if ($l['fit'] < self::MIN_FIT) {
            return ['state' => 'no_trend', 'per_week' => $perWeek];
        }
        $days = ($limit - $now) / $l['slope'];
        return ['state' => 'growing', 'per_week' => $perWeek, 'days' => max(0, (int) ceil($days)), 'beyond_horizon' => $days > self::HORIZON];
    }

    /**
     * @return array<string, mixed>
     */
    public function forecast(?int $now = null): array
    {
        $now ??= time();
        $sid = $this->server->id();
        $rows = $this->db->fetchAll(
            "SELECT CAST(taken_at / 86400 AS INTEGER) AS d, MAX(disk) AS disk, AVG(mem) AS mem, AVG(cpu) AS cpu, AVG(load1) AS load1, COUNT(*) AS n
             FROM metric_samples WHERE server_id = ? AND taken_at >= ? GROUP BY d ORDER BY d",
            [$sid, $now - 30 * 86400]
        );
        // Days with a handful of samples are not representative.
        $rows = array_values(array_filter($rows, static fn (array $r): bool => (int) $r['n'] >= 12));
        if ($rows === []) {
            return ['days' => 0, '_state' => 'empty'];
        }
        $d0 = (int) $rows[0]['d'];
        $series = static function (string $col) use ($rows, $d0): array {
            return array_map(static fn (array $r): array => [(float) ((int) $r['d'] - $d0), (float) $r[$col]], $rows);
        };
        $lastRow = $rows[count($rows) - 1];
        $cur = $this->db->fetch('SELECT disk, mem, cpu, load1, taken_at FROM metric_samples WHERE server_id = ? ORDER BY taken_at DESC LIMIT 1', [$sid]);
        $week = $now - 7 * 86400;
        $avg = $this->db->fetch('SELECT AVG(cpu) AS cpu, MAX(cpu) AS cpu_peak, AVG(load1) AS load1, MAX(load1) AS load_peak, AVG(mem) AS mem, MAX(mem) AS mem_peak FROM metric_samples WHERE server_id = ? AND taken_at >= ?', [$sid, $week]);
        $prev = $this->db->fetch('SELECT AVG(cpu) AS cpu, AVG(load1) AS load1 FROM metric_samples WHERE server_id = ? AND taken_at >= ? AND taken_at < ?', [$sid, $week - 7 * 86400, $week]);
        $f = static fn (mixed $v): ?float => $v === null ? null : round((float) $v, 1);
        return [
            'days' => count($rows),
            'disk' => ['now' => $f($cur['disk'] ?? null), 'forecast' => self::daysToLimit($series('disk'), 100.0)],
            'mem' => ['now' => $f($cur['mem'] ?? null), 'avg' => $f($avg['mem'] ?? null), 'peak' => $f($avg['mem_peak'] ?? null), 'forecast' => self::daysToLimit($series('mem'), 95.0)],
            'cpu' => ['now' => $f($cur['cpu'] ?? null), 'avg' => $f($avg['cpu'] ?? null), 'peak' => $f($avg['cpu_peak'] ?? null), 'prev_avg' => $f($prev['cpu'] ?? null)],
            'load' => ['avg' => $f($avg['load1'] ?? null), 'peak' => $f($avg['load_peak'] ?? null), 'prev_avg' => $f($prev['load1'] ?? null)],
            'last_sample' => (int) ($cur['taken_at'] ?? $lastRow['d'] * 86400),
            '_state' => 'ok',
        ];
    }

    /**
     * Network use in the last 24 hours (estimated from the five-minute samples)
     * and the websites that sent the most this month.
     *
     * @return array<string, mixed>
     */
    public function traffic(?int $now = null): array
    {
        $now ??= time();
        $sid = $this->server->id();
        $rows = $this->db->fetchAll('SELECT taken_at, rx_bps, tx_bps FROM metric_samples WHERE server_id = ? AND taken_at >= ? ORDER BY taken_at', [$sid, $now - 86400]);
        $net = null;
        if (count($rows) >= 12) {
            $rx = $tx = 0.0;
            $rxPeak = $txPeak = 0.0;
            $prev = null;
            foreach ($rows as $r) {
                $dt = $prev === null ? 0 : min(900, (int) $r['taken_at'] - $prev);
                $rx += (float) $r['rx_bps'] * $dt;
                $tx += (float) $r['tx_bps'] * $dt;
                $rxPeak = max($rxPeak, (float) $r['rx_bps']);
                $txPeak = max($txPeak, (float) $r['tx_bps']);
                $prev = (int) $r['taken_at'];
            }
            $span = max(1, (int) end($rows)['taken_at'] - (int) $rows[0]['taken_at']);
            $net = ['rx_bytes' => (int) $rx, 'tx_bytes' => (int) $tx, 'rx_avg' => $rx / $span, 'tx_avg' => $tx / $span, 'rx_peak' => $rxPeak, 'tx_peak' => $txPeak, 'hours' => round($span / 3600, 1)];
        }
        $month = gmdate('Y-m', $now);
        $sites = $this->db->fetchAll(
            'SELECT w.domain, u.bw_bytes FROM website_usage u JOIN websites w ON w.id = u.website_id WHERE w.server_id = ? AND u.bw_month = ? AND u.bw_bytes > 0 ORDER BY u.bw_bytes DESC LIMIT 5',
            [$sid, $month]
        );
        $total = (int) $this->db->scalar('SELECT COALESCE(SUM(u.bw_bytes), 0) FROM website_usage u JOIN websites w ON w.id = u.website_id WHERE w.server_id = ? AND u.bw_month = ?', [$sid, $month]);
        $measured = (int) $this->db->scalar('SELECT COUNT(*) FROM website_usage u JOIN websites w ON w.id = u.website_id WHERE w.server_id = ? AND u.bw_month = ?', [$sid, $month]);
        return [
            'net' => $net,
            'month' => $month,
            'sites' => array_map(static fn (array $r): array => ['domain' => (string) $r['domain'], 'bytes' => (int) $r['bw_bytes']], $sites),
            'sites_total' => $total,
            'sites_measured' => $measured,
            '_state' => $net === null && $measured === 0 ? 'empty' : 'ok',
        ];
    }
}
