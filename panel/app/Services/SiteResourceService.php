<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;
use NimVor\Hosting\Scope;

/**
 * What each website uses, recorded with the existing monitoring pass (no second collector).
 *
 * Sources (agent action sites.metrics): the PHP-FPM workers of the website's own pool (CPU time, memory, disk
 * bytes: sampled), the website's own access log (requests, status classes, bytes sent) and the cgroup v2 counters
 * of the website's limits account (exact, but per account: the websites of one account share a slice).
 * Nothing is estimated: what was not measured is NULL and charts show a gap.
 */
final class SiteResourceService
{
    /** Range => [seconds, source, bucket seconds]. Raw samples feed 1h and 24h; hourly roll-ups feed the rest. */
    public const RANGES = ['1h' => [3600, 'raw', 60], '24h' => [86400, 'raw', 900], '7d' => [604800, 'hourly', 3600], '30d' => [2592000, 'hourly', 21600]];
    /** Ranking sort keys => SQL expression over the aggregated columns. */
    private const SORTS = [
        'cpu' => 'cpu', 'memory' => 'mem_max', 'io' => 'io_b', 'requests' => 'req', 'traffic' => 'bytes', 'errors' => 'errs', 'faults' => 'faults',
    ];
    /** An account is "near" a limit from this share of it (a warning, not a fault). */
    private const NEAR = 0.9;
    /** Events of one kind within this many seconds are one event. */
    private const EVENT_GAP = 900;

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
        private readonly SettingsService $settings,
    ) {
    }

    // ---- collection (one call per monitoring pass) --------------------------------------------------------------

    /** @return list<string> log lines of the pass */
    public function collect(?int $now = null): array
    {
        if (!$this->settings->bool('resource_monitoring')) {
            return [];
        }
        $now ??= time();
        $sid = $this->server->id();
        $ids = [];
        foreach ($this->db->fetchAll('SELECT id, domain FROM websites WHERE server_id = ? AND enabled = 1', [$sid]) as $r) {
            $ids[(string) $r['domain']] = (int) $r['id'];
        }
        if ($ids === []) {
            return [];
        }
        try {
            $res = (array) $this->agent->call('sites.metrics', ['domains' => array_keys($ids)], 60);
        } catch (AgentException $e) {
            return ['website resources: not collected (' . $e->getMessage() . ')'];
        }
        $n = 0;
        $this->db->transaction(function () use ($res, $ids, $now, $sid, &$n): void {
            $interval = (float) ($res['interval_seconds'] ?? 0);
            foreach ((array) ($res['sites'] ?? []) as $s) {
                $s = (array) $s;
                $wid = $ids[(string) ($s['domain'] ?? '')] ?? null;
                if ($wid === null) {
                    continue;
                }
                $php = isset($s['php']) ? (array) $s['php'] : null;
                $web = isset($s['web']) ? (array) $s['web'] : null;
                if ($php === null && $web === null) {
                    continue; // nothing was measured for this website: no row, not a row of zeros
                }
                $rates = $interval > 0;
                $this->db->run(
                    'INSERT INTO site_metric_samples (server_id, website_id, taken_at, account, interval_s, workers, mem_bytes, cpu_s, read_b, write_b, exited, req, s2, s3, s4, s5, bytes_sent)
                     VALUES (:sid, :w, :t, :acct, :iv, :workers, :mem, :cpu, :rb, :wb, :ex, :req, :s2, :s3, :s4, :s5, :bytes)',
                    [
                        'sid' => $sid, 'w' => $wid, 't' => $now, 'acct' => ($s['account'] ?? '') !== '' ? (string) $s['account'] : null, 'iv' => $interval,
                        'workers' => $php === null ? null : (int) ($php['workers'] ?? 0), 'mem' => $php === null ? null : (int) ($php['memory_bytes'] ?? 0),
                        'cpu' => $php !== null && $rates ? (float) ($php['cpu_seconds'] ?? 0) : null,
                        'rb' => $php !== null && $rates ? (int) ($php['read_bytes'] ?? 0) : null, 'wb' => $php !== null && $rates ? (int) ($php['write_bytes'] ?? 0) : null,
                        'ex' => $php !== null && $rates ? (int) ($php['workers_exited'] ?? 0) : null,
                        'req' => $web !== null && $rates ? (int) ($web['requests'] ?? 0) : null,
                        's2' => $web !== null && $rates ? (int) ($web['status_2xx'] ?? 0) : null, 's3' => $web !== null && $rates ? (int) ($web['status_3xx'] ?? 0) : null,
                        's4' => $web !== null && $rates ? (int) ($web['status_4xx'] ?? 0) : null, 's5' => $web !== null && $rates ? (int) ($web['status_5xx'] ?? 0) : null,
                        'bytes' => $web !== null && $rates ? (int) ($web['bytes_sent'] ?? 0) : null,
                    ]
                );
                $n++;
            }
            foreach ((array) ($res['accounts'] ?? []) as $a) {
                $this->ingestAccount((array) $a, $now, $interval, $sid);
            }
        });
        $this->rollup($now);
        $this->prune($now);
        return ['website resources: ' . $n . ' website' . ($n === 1 ? '' : 's') . ' sampled'];
    }

    /** @param array<string, mixed> $a */
    private function ingestAccount(array $a, int $now, float $interval, int $sid): void
    {
        $account = (string) ($a['account'] ?? '');
        $u = (array) ($a['usage'] ?? []);
        if ($account === '' || empty($u['active'])) {
            return; // no slice (the account has no limits): the kernel keeps nothing to read
        }
        $lim = (array) ($a['limits'] ?? []);
        $cur = [
            'cpu' => (int) ($u['cpu_usage_usec'] ?? 0), 'thr' => (int) ($u['cpu_throttled_usec'] ?? 0), 'nthr' => (int) ($u['cpu_nr_throttled'] ?? 0),
            'high' => (int) ($u['memory_high_events'] ?? 0), 'max' => (int) ($u['memory_max_events'] ?? 0), 'oom' => (int) ($u['oom_kills'] ?? 0),
            'r' => (int) ($u['io_read_bytes'] ?? 0), 'w' => (int) ($u['io_write_bytes'] ?? 0), 'rios' => (int) ($u['io_read_ops'] ?? 0), 'wios' => (int) ($u['io_write_ops'] ?? 0),
        ];
        $prev = $this->db->fetch('SELECT taken_at, counters FROM account_metric_state WHERE server_id = ? AND account = ?', [$sid, $account]);
        $this->db->run('INSERT OR REPLACE INTO account_metric_state (server_id, account, taken_at, counters) VALUES (?, ?, ?, ?)', [$sid, $account, $now, json_encode($cur)]);
        // Counters are cumulative: the interval's use is the difference, and only when nothing reset in between.
        $d = null;
        if ($prev !== null && $interval > 0 && $now - (int) $prev['taken_at'] > 0 && $now - (int) $prev['taken_at'] <= 3600) {
            $p = (array) json_decode((string) $prev['counters'], true);
            $ok = true;
            $d = [];
            foreach ($cur as $k => $v) {
                $pv = (int) ($p[$k] ?? 0);
                if ($v < $pv) {
                    $ok = false;
                    break;
                }
                $d[$k] = $v - $pv;
            }
            $d = $ok ? $d : null;
            $interval = (float) ($now - (int) $prev['taken_at']);
        }
        $mem = (int) ($u['memory_bytes'] ?? 0);
        $peak = !empty($u['memory_peak_known']) ? (int) $u['memory_peak_bytes'] : null;
        $tasks = (int) ($u['tasks'] ?? 0);
        $memLimit = (int) ($lim['memory_mb'] ?? 0);
        $cpuLimit = (int) ($lim['cpu_percent'] ?? 0);
        $taskLimit = (int) ($lim['tasks_max'] ?? 0);
        $this->db->run(
            'INSERT INTO account_metric_samples (server_id, account, taken_at, interval_s, mem_bytes, mem_peak, tasks, mem_limit_mb, cpu_limit_pct, tasks_limit, cpu_s, throttled_s, nr_throttled, mem_high, mem_max, oom_kills, io_r, io_w, io_rios, io_wios)
             VALUES (:sid, :a, :t, :iv, :mem, :peak, :tasks, :ml, :cl, :tl, :cpu, :thr, :nthr, :high, :max, :oom, :r, :w, :rios, :wios)',
            [
                'sid' => $sid, 'a' => $account, 't' => $now, 'iv' => $d === null ? 0 : $interval, 'mem' => $mem, 'peak' => $peak, 'tasks' => $tasks,
                'ml' => $memLimit ?: null, 'cl' => $cpuLimit ?: null, 'tl' => $taskLimit ?: null,
                'cpu' => $d === null ? null : $d['cpu'] / 1e6, 'thr' => $d === null ? null : $d['thr'] / 1e6, 'nthr' => $d['nthr'] ?? null,
                'high' => $d['high'] ?? null, 'max' => $d['max'] ?? null, 'oom' => $d['oom'] ?? null,
                'r' => $d['r'] ?? null, 'w' => $d['w'] ?? null, 'rios' => $d['rios'] ?? null, 'wios' => $d['wios'] ?? null,
            ]
        );
        // Faults: a limit was actually hit in this interval. Warnings: a threshold was crossed, nothing was cut off.
        if ($d !== null) {
            if ($d['oom'] > 0) {
                $this->event($sid, $account, 'oom_kill', 'fault', $d['oom'] . ' process' . ($d['oom'] === 1 ? ' was' : 'es were') . ' killed for lack of memory', $now);
            }
            if ($d['max'] > 0) {
                $this->event($sid, $account, 'memory_limit', 'fault', 'The memory limit' . ($memLimit ? ' (' . $memLimit . ' MB)' : '') . ' was reached ' . $d['max'] . ' time' . ($d['max'] === 1 ? '' : 's'), $now);
            }
            if ($d['nthr'] > 0) {
                $this->event($sid, $account, 'cpu_throttled', 'fault', 'CPU was throttled in ' . $d['nthr'] . ' period' . ($d['nthr'] === 1 ? '' : 's') . ' (' . round($d['thr'] / 1e6 * 1000) . ' ms)', $now);
            }
            if ($d['high'] > 0 && $d['max'] === 0) {
                $this->event($sid, $account, 'memory_pressure', 'warning', 'Memory use passed the soft limit ' . $d['high'] . ' time' . ($d['high'] === 1 ? '' : 's') . '; processes were slowed to reclaim memory', $now);
            }
            if ($cpuLimit > 0 && $interval > 0 && ($d['cpu'] / 1e6) / $interval * 100 >= self::NEAR * $cpuLimit && $d['nthr'] === 0) {
                $this->event($sid, $account, 'cpu_near_limit', 'warning', sprintf('CPU use was %.0f%% of one core against a limit of %d%%', ($d['cpu'] / 1e6) / $interval * 100, $cpuLimit), $now);
            }
        }
        if ($memLimit > 0 && $mem >= self::NEAR * $memLimit * 1048576) {
            $this->event($sid, $account, 'memory_near_limit', 'warning', sprintf('Memory use is %.0f%% of the %d MB limit', $mem / ($memLimit * 1048576) * 100, $memLimit), $now);
        }
        if ($taskLimit > 0 && $tasks >= self::NEAR * $taskLimit) {
            $this->event($sid, $account, 'processes_near_limit', 'warning', $tasks . ' of ' . $taskLimit . ' processes allowed', $now);
        }
    }

    private function event(int $sid, string $account, string $kind, string $severity, string $detail, int $now): void
    {
        $open = $this->db->fetch('SELECT id FROM site_resource_events WHERE server_id = ? AND account = ? AND kind = ? AND last_at >= ? ORDER BY last_at DESC LIMIT 1', [$sid, $account, $kind, $now - self::EVENT_GAP]);
        if ($open !== null) {
            $this->db->run('UPDATE site_resource_events SET last_at = ?, occurrences = occurrences + 1, detail = ? WHERE id = ?', [$now, $detail, (int) $open['id']]);
            return;
        }
        $this->db->run('INSERT INTO site_resource_events (server_id, account, kind, severity, first_at, last_at, detail) VALUES (?, ?, ?, ?, ?, ?, ?)', [$sid, $account, $kind, $severity, $now, $now, $detail]);
    }

    /** The current and the previous hour are (re)built from the raw samples: cheap, and a late sample is not lost. */
    public function rollup(int $now): void
    {
        $this->db->transaction(function () use ($now): void {
            foreach ([intdiv($now, 3600) * 3600 - 3600, intdiv($now, 3600) * 3600] as $b) {
                $this->db->run(
                    'INSERT OR REPLACE INTO site_metric_hourly (website_id, bucket, samples, secs_php, secs_web, cpu_s, read_b, write_b, mem_avg, mem_max, workers_max, req, s2, s3, s4, s5, bytes_sent)
                     SELECT website_id, :b, COUNT(*), COALESCE(SUM(CASE WHEN cpu_s IS NOT NULL THEN interval_s END), 0), COALESCE(SUM(CASE WHEN req IS NOT NULL THEN interval_s END), 0),
                            SUM(cpu_s), SUM(read_b), SUM(write_b), AVG(mem_bytes), MAX(mem_bytes), MAX(workers), SUM(req), SUM(s2), SUM(s3), SUM(s4), SUM(s5), SUM(bytes_sent)
                     FROM site_metric_samples WHERE server_id = :sid AND taken_at >= :b AND taken_at < :e GROUP BY website_id',
                    ['b' => $b, 'e' => $b + 3600, 'sid' => $this->server->id()]
                );
            }
        });
    }

    public function prune(int $now): void
    {
        $raw = $now - $this->settings->int('resource_raw_days') * 86400;
        $hourly = $now - $this->settings->int('resource_hourly_days') * 86400;
        $this->db->run('DELETE FROM site_metric_samples WHERE taken_at < ?', [$raw]);
        $this->db->run('DELETE FROM account_metric_samples WHERE taken_at < ?', [$raw]);
        $this->db->run('DELETE FROM site_metric_hourly WHERE bucket < ?', [$hourly]);
        $this->db->run('DELETE FROM site_resource_events WHERE last_at < ?', [$hourly]);
    }

    // ---- reading --------------------------------------------------------------------------------------------------

    public static function range(string $range): string
    {
        return isset(self::RANGES[$range]) ? $range : '24h';
    }

    /**
     * Chart points for one website. A value that was not measured in a bucket is null.
     *
     * @return list<array<string, float|int|null>>
     */
    public function series(int $websiteId, string $range, ?int $now = null): array
    {
        $now ??= time();
        [$seconds, $source, $size] = self::RANGES[self::range($range)];
        $from = $now - $seconds;
        if ($source === 'raw') {
            $sql = 'SELECT (taken_at - :from) / :size AS b, MIN(taken_at) AS t,
                        SUM(cpu_s) AS cpu_s, SUM(CASE WHEN cpu_s IS NOT NULL THEN interval_s END) AS secs_php, SUM(CASE WHEN req IS NOT NULL THEN interval_s END) AS secs_web,
                        AVG(mem_bytes) AS mem, MAX(mem_bytes) AS mem_max, AVG(workers) AS workers, MAX(workers) AS workers_max, SUM(read_b) AS rb, SUM(write_b) AS wb,
                        SUM(req) AS req, SUM(s4) AS s4, SUM(s5) AS s5, SUM(bytes_sent) AS bytes
                    FROM site_metric_samples WHERE website_id = :w AND taken_at >= :from GROUP BY b ORDER BY b';
        } else {
            $sql = 'SELECT (bucket - :from) / :size AS b, MIN(bucket) AS t,
                        SUM(cpu_s) AS cpu_s, SUM(secs_php) AS secs_php, SUM(secs_web) AS secs_web,
                        AVG(mem_avg) AS mem, MAX(mem_max) AS mem_max, NULL AS workers, MAX(workers_max) AS workers_max, SUM(read_b) AS rb, SUM(write_b) AS wb,
                        SUM(req) AS req, SUM(s4) AS s4, SUM(s5) AS s5, SUM(bytes_sent) AS bytes
                    FROM site_metric_hourly WHERE website_id = :w AND bucket >= :from GROUP BY b ORDER BY b';
        }
        $rows = $this->db->fetchAll($sql, ['from' => $from, 'size' => $size, 'w' => $websiteId]);
        return array_map(static function (array $r): array {
            $php = (float) ($r['secs_php'] ?? 0);
            $web = (float) ($r['secs_web'] ?? 0);
            $f = static fn (mixed $v, float $div, int $dec = 2, float $mul = 1.0): ?float => $v === null || $div <= 0 ? null : round((float) $v / $div * $mul, $dec);
            $i = static fn (mixed $v): ?int => $v === null ? null : (int) round((float) $v);
            return [
                't' => (int) $r['t'],
                'cpu' => $f($r['cpu_s'], $php, 2, 100.0),            // percent of one CPU core
                'cpu_seconds' => $r['cpu_s'] === null ? null : round((float) $r['cpu_s'], 2),
                'mem' => $i($r['mem']), 'mem_max' => $i($r['mem_max']),
                'workers' => $r['workers'] === null ? null : round((float) $r['workers'], 1), 'workers_max' => $i($r['workers_max']),
                'read' => $f($r['rb'], $php, 0), 'write' => $f($r['wb'], $php, 0),   // bytes per second
                'rpm' => $f($r['req'], $web, 2, 60.0),
                'bw_out' => $f($r['bytes'], $web, 0),               // response bytes per second
                'e4' => $f($r['s4'], $web, 2, 60.0), 'e5' => $f($r['s5'], $web, 2, 60.0),   // per minute
            ];
        }, $rows);
    }

    /** What the account's slice did, for the limits panel (raw history only: see resource_raw_days). @return list<array<string, mixed>> */
    public function accountSeries(string $account, string $range, ?int $now = null): array
    {
        $now ??= time();
        [$seconds, , $size] = self::RANGES[self::range($range)];
        $seconds = min($seconds, $this->settings->int('resource_raw_days') * 86400);
        $size = max($size, 60);
        $rows = $this->db->fetchAll(
            'SELECT (taken_at - :from) / :size AS b, MIN(taken_at) AS t, AVG(mem_bytes) AS mem, MAX(mem_peak) AS peak, MAX(mem_limit_mb) AS ml,
                    SUM(cpu_s) AS cpu_s, SUM(CASE WHEN cpu_s IS NOT NULL THEN interval_s END) AS secs, MAX(cpu_limit_pct) AS cl, SUM(nr_throttled) AS thr,
                    SUM(io_r) AS r, SUM(io_w) AS w, SUM(io_rios) AS rios, SUM(io_wios) AS wios
             FROM account_metric_samples WHERE server_id = :sid AND account = :a AND taken_at >= :from GROUP BY b ORDER BY b',
            ['from' => $now - $seconds, 'size' => $size, 'a' => $account, 'sid' => $this->server->id()]
        );
        return array_map(static function (array $r): array {
            $secs = (float) ($r['secs'] ?? 0);
            $per = static fn (mixed $v): ?float => $v === null || $secs <= 0 ? null : round((float) $v / $secs, 1);
            return [
                't' => (int) $r['t'], 'mem' => $r['mem'] === null ? null : (int) $r['mem'], 'mem_peak' => $r['peak'] === null ? null : (int) $r['peak'], 'mem_limit_mb' => $r['ml'] === null ? null : (int) $r['ml'],
                'cpu' => $secs > 0 && $r['cpu_s'] !== null ? round((float) $r['cpu_s'] / $secs * 100, 1) : null, 'cpu_limit_pct' => $r['cl'] === null ? null : (int) $r['cl'],
                'throttled' => $r['thr'] === null ? null : (int) $r['thr'],
                'io_read_bps' => $per($r['r']), 'io_write_bps' => $per($r['w']), 'riops' => $per($r['rios']), 'wiops' => $per($r['wios']),
            ];
        }, $rows);
    }

    /** The latest readings of a website and of its account's slice. @return array<string, mixed> */
    public function current(int $websiteId): array
    {
        $s = $this->db->fetch('SELECT * FROM site_metric_samples WHERE website_id = ? ORDER BY taken_at DESC LIMIT 1', [$websiteId]);
        $a = $s !== null && ($s['account'] ?? '') !== '' ? $this->db->fetch('SELECT * FROM account_metric_samples WHERE server_id = ? AND account = ? ORDER BY taken_at DESC LIMIT 1', [$this->server->id(), (string) $s['account']]) : null;
        $shared = $s !== null && ($s['account'] ?? '') !== '' ? (int) $this->db->scalar('SELECT COUNT(DISTINCT website_id) FROM site_metric_samples WHERE account = ? AND taken_at >= ?', [(string) $s['account'], (int) $s['taken_at'] - 900]) : 0;
        return ['site' => $s, 'account' => $a, 'sites_in_account' => $shared];
    }

    /** @return list<array<string, mixed>> */
    public function events(int $websiteId, string $range, int $limit = 50, ?int $now = null): array
    {
        $now ??= time();
        $acct = $this->db->scalar('SELECT account FROM site_metric_samples WHERE website_id = ? AND account IS NOT NULL ORDER BY taken_at DESC LIMIT 1', [$websiteId]);
        if (!is_string($acct) || $acct === '') {
            return [];
        }
        return $this->db->fetchAll(
            'SELECT kind, severity, first_at, last_at, occurrences, detail FROM site_resource_events WHERE server_id = ? AND account = ? AND last_at >= ? ORDER BY severity = \'fault\' DESC, last_at DESC LIMIT ' . max(1, min(200, $limit)),
            [$this->server->id(), $acct, $now - self::RANGES[self::range($range)][0]]
        );
    }

    /**
     * The ranking of websites by what they used in a range, limited to the websites the user may see.
     *
     * @param array<string, mixed> $user
     * @param array{q?: string, faults?: bool} $filter
     * @return list<array<string, mixed>>
     */
    public function ranking(array $user, string $range, string $sort = 'cpu', array $filter = [], int $limit = 100, ?int $now = null): array
    {
        $now ??= time();
        $range = self::range($range);
        $sort = isset(self::SORTS[$sort]) ? $sort : 'cpu';
        [$seconds, $source] = self::RANGES[$range];
        $from = $now - $seconds;
        [$cond, $params] = Scope::condition($user, 'w.owner_id');
        if ($source === 'raw' && $range === '1h') {
            $agg = 'SELECT website_id, SUM(cpu_s) AS cpu_s, SUM(CASE WHEN cpu_s IS NOT NULL THEN interval_s END) AS secs_php, SUM(CASE WHEN req IS NOT NULL THEN interval_s END) AS secs_web,
                           AVG(mem_bytes) AS mem_avg, MAX(mem_bytes) AS mem_max, SUM(read_b) + SUM(write_b) AS io_b, SUM(req) AS req, SUM(bytes_sent) AS bytes, SUM(s4) AS s4, SUM(s5) AS s5
                    FROM site_metric_samples WHERE taken_at >= :from GROUP BY website_id';
        } else {
            $agg = 'SELECT website_id, SUM(cpu_s) AS cpu_s, SUM(secs_php) AS secs_php, SUM(secs_web) AS secs_web,
                           AVG(mem_avg) AS mem_avg, MAX(mem_max) AS mem_max, SUM(read_b) + SUM(write_b) AS io_b, SUM(req) AS req, SUM(bytes_sent) AS bytes, SUM(s4) AS s4, SUM(s5) AS s5
                    FROM site_metric_hourly WHERE bucket >= :from GROUP BY website_id';
        }
        $where = $cond;
        if (($filter['q'] ?? '') !== '') {
            $where .= ' AND w.domain LIKE :q ESCAPE \'\\\'';
            $params['q'] = '%' . addcslashes((string) $filter['q'], '%_\\') . '%';
        }
        $rows = $this->db->fetchAll(
            "SELECT w.id, w.domain, u.username AS owner, h.cpu_s, h.secs_php, h.secs_web, h.mem_avg, h.mem_max, h.io_b, h.req, h.bytes, h.s4, h.s5,
                    CASE WHEN h.secs_php > 0 THEN h.cpu_s / h.secs_php * 100 END AS cpu,
                    COALESCE(h.s4, 0) + COALESCE(h.s5, 0) AS errs,
                    (SELECT account FROM site_metric_samples s WHERE s.website_id = w.id AND s.account IS NOT NULL ORDER BY s.taken_at DESC LIMIT 1) AS account,
                    (SELECT COUNT(*) FROM site_resource_events e WHERE e.severity = 'fault' AND e.last_at >= :from AND e.account = (SELECT account FROM site_metric_samples s2 WHERE s2.website_id = w.id AND s2.account IS NOT NULL ORDER BY s2.taken_at DESC LIMIT 1)) AS faults
             FROM websites w LEFT JOIN users u ON u.id = w.owner_id JOIN ({$agg}) h ON h.website_id = w.id
             WHERE w.server_id = :sid AND {$where}",
            ['from' => $from, 'sid' => $this->server->id()] + $params
        );
        if (!empty($filter['faults'])) {
            $rows = array_values(array_filter($rows, static fn (array $r): bool => (int) $r['faults'] > 0));
        }
        $key = self::SORTS[$sort];
        usort($rows, static function (array $a, array $b) use ($key): int {
            $x = $a[$key] === null ? -1.0 : (float) $a[$key];
            $y = $b[$key] === null ? -1.0 : (float) $b[$key];
            return $y <=> $x ?: strcmp((string) $a['domain'], (string) $b['domain']);
        });
        return array_map(static fn (array $r): array => [
            'id' => (int) $r['id'], 'domain' => (string) $r['domain'], 'owner' => $r['owner'], 'account' => $r['account'],
            'cpu' => $r['cpu'] === null ? null : round((float) $r['cpu'], 1), 'cpu_seconds' => $r['cpu_s'] === null ? null : round((float) $r['cpu_s'], 1),
            'mem_avg' => $r['mem_avg'] === null ? null : (int) $r['mem_avg'], 'mem_max' => $r['mem_max'] === null ? null : (int) $r['mem_max'],
            'io_bytes' => $r['io_b'] === null ? null : (int) $r['io_b'], 'requests' => $r['req'] === null ? null : (int) $r['req'],
            'bytes_sent' => $r['bytes'] === null ? null : (int) $r['bytes'], 'e4' => $r['s4'] === null ? null : (int) $r['s4'], 'e5' => $r['s5'] === null ? null : (int) $r['s5'],
            'errors' => $r['req'] === null ? null : (int) $r['errs'], 'faults' => (int) $r['faults'],
        ], array_slice($rows, 0, max(1, min(500, $limit))));
    }
}
