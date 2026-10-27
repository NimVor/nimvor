<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;

/**
 * Website statistics (AWStats, updated every 6 hours as the website's
 * account). The report is drawn by the panel from AWStats' plain-text data
 * file; AWStats' own HTML is never shown.
 */
final class StatsService
{
    public function __construct(
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * @param array<string, mixed> $site
     * @return array<string, mixed>
     */
    private static function target(array $site): array
    {
        return ['domain' => $site['domain'], 'aliases' => array_values($site['aliases']), 'user' => $site['system_user'], 'root' => $site['root']];
    }

    /**
     * @param array<string, mixed> $site
     * @return array{installed: bool, enabled: bool, months: list<string>, updated_at: ?string}
     */
    public function status(array $site): array
    {
        $s = (array) $this->agent->call('stats.status', self::target($site));
        return [
            'installed' => !empty($s['installed']), 'enabled' => !empty($s['enabled']),
            'months' => array_values(array_filter(array_map('strval', (array) ($s['months'] ?? [])), static fn (string $m): bool => preg_match('/^[0-9]{6}$/', $m) === 1)),
            'updated_at' => isset($s['updated_at']) ? (string) $s['updated_at'] : null,
        ];
    }

    /** @param array<string, mixed> $site  $audit false: housekeeping (website deletion) */
    public function setEnabled(array $site, bool $enabled, bool $audit = true): void
    {
        $this->agent->call('stats.enable', self::target($site) + ['enabled' => $enabled]);
        if (!$audit) {
            return;
        }
        $this->audit->record($enabled ? 'stats.enable' : 'stats.disable', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain']]);
    }

    /**
     * @param array<string, mixed> $site
     * @return string job id
     */
    public function update(array $site): string
    {
        $res = (array) $this->agent->call('stats.update', self::target($site));
        return (string) $res['job_id'];
    }

    /**
     * @param array<string, mixed> $site
     * @return array<string, mixed>|null the month's report, null without data
     */
    public function report(array $site, string $month): ?array
    {
        $res = (array) $this->agent->call('stats.data', self::target($site) + ['month' => $month]);
        return empty($res['found']) ? null : self::parse((string) $res['content']);
    }

    /**
     * AWStats data file → report. Sections are "BEGIN_X n" … "END_X" with
     * space-separated rows; unknown sections and malformed rows are skipped.
     *
     * @return array<string, mixed>
     */
    public static function parse(string $data): array
    {
        $sections = [];
        $cur = null;
        foreach (preg_split('/\r?\n/', $data) ?: [] as $line) {
            if (preg_match('/^BEGIN_([A-Z_]+)\b/', $line, $m)) {
                $cur = $m[1];
                $sections[$cur] = [];
            } elseif (str_starts_with($line, 'END_')) {
                $cur = null;
            } elseif ($cur !== null && $line !== '' && $line[0] !== '#') {
                $sections[$cur][] = preg_split('/ /', $line) ?: [];
            }
        }
        $n = static fn (array $r, int $i): int => (int) ($r[$i] ?? 0);
        $general = [];
        foreach ($sections['GENERAL'] ?? [] as $r) {
            $general[(string) $r[0]] = (string) ($r[1] ?? '');
        }
        $days = [];
        foreach ($sections['DAY'] ?? [] as $r) {
            if (preg_match('/^[0-9]{8}$/', (string) $r[0])) {
                $days[(string) $r[0]] = ['pages' => $n($r, 1), 'hits' => $n($r, 2), 'bytes' => $n($r, 3), 'visits' => $n($r, 4)];
            }
        }
        ksort($days);
        $hours = array_fill(0, 24, ['pages' => 0, 'hits' => 0, 'bytes' => 0]);
        foreach ($sections['TIME'] ?? [] as $r) {
            $h = (int) $r[0];
            if ($h >= 0 && $h < 24) {
                $hours[$h] = ['pages' => $n($r, 1), 'hits' => $n($r, 2), 'bytes' => $n($r, 3)];
            }
        }
        $top = static function (array $rows, int $valueIdx, int $limit, callable $label) use ($n): array {
            $out = [];
            foreach ($rows as $r) {
                $out[] = ['label' => $label((string) $r[0]), 'value' => $n($r, $valueIdx)];
            }
            usort($out, static fn (array $a, array $b): int => $b['value'] <=> $a['value']);
            return array_slice($out, 0, $limit);
        };
        $plain = static fn (string $s): string => $s;
        $totals = ['visits' => 0, 'pages' => 0, 'hits' => 0, 'bytes' => 0];
        foreach ($days as $d) {
            foreach ($totals as $k => $_) {
                $totals[$k] += $d[$k];
            }
        }
        return [
            'unique' => (int) ($general['TotalUnique'] ?? 0),
            'visits' => max($totals['visits'], (int) ($general['TotalVisits'] ?? 0)),
            'pages' => $totals['pages'], 'hits' => $totals['hits'], 'bytes' => $totals['bytes'],
            'updated' => self::stamp((string) ($general['LastUpdate'] ?? '')),
            'days' => $days,
            'hours' => $hours,
            'pages_top' => $top($sections['SIDER'] ?? [], 1, 25, $plain),
            'referrers' => $top($sections['PAGEREFS'] ?? [], 1, 15, $plain),
            'search' => $top($sections['SEREFERRALS'] ?? [], 1, 10, static fn (string $e): string => preg_replace('/^www\./', '', str_replace('_', '.', $e)) ?? $e),
            'browsers' => self::merge($top($sections['BROWSER'] ?? [], 1, 200, [self::class, 'browser']), 10),
            'systems' => self::merge($top($sections['OS'] ?? [], 1, 200, [self::class, 'system']), 10),
            'countries' => $top($sections['DOMAIN'] ?? [], 2, 15, static fn (string $c): string => in_array($c, ['ip', 'unknown'], true) ? 'Unknown' : strtoupper($c)),
            'errors' => $top($sections['ERRORS'] ?? [], 1, 10, $plain),
        ];
    }

    /** "20260927153000" → ISO time, or null. */
    private static function stamp(string $s): ?string
    {
        return preg_match('/^([0-9]{4})([0-9]{2})([0-9]{2})([0-9]{2})([0-9]{2})([0-9]{2})/', $s, $m) ? "{$m[1]}-{$m[2]}-{$m[3]}T{$m[4]}:{$m[5]}:{$m[6]}Z" : null;
    }

    /**
     * Sum rows that got the same friendly label (e.g. all Chrome versions).
     *
     * @param list<array{label: string, value: int}> $rows
     * @return list<array{label: string, value: int}>
     */
    private static function merge(array $rows, int $limit): array
    {
        $sum = [];
        foreach ($rows as $r) {
            $sum[$r['label']] = ($sum[$r['label']] ?? 0) + $r['value'];
        }
        arsort($sum);
        $out = [];
        foreach (array_slice($sum, 0, $limit, true) as $label => $value) {
            $out[] = ['label' => (string) $label, 'value' => $value];
        }
        return $out;
    }

    public static function browser(string $id): string
    {
        foreach (['msedge' => 'Edge', 'edge' => 'Edge', 'chrome' => 'Chrome', 'firefox' => 'Firefox', 'safari' => 'Safari', 'opera' => 'Opera', 'msie' => 'Internet Explorer', 'samsung' => 'Samsung Internet'] as $p => $name) {
            if (str_starts_with($id, $p)) {
                return $name;
            }
        }
        return $id === 'Unknown' || $id === 'unknown' ? 'Unknown' : $id;
    }

    public static function system(string $id): string
    {
        foreach (['win' => 'Windows', 'macos' => 'macOS', 'ios' => 'iOS', 'android' => 'Android', 'linux' => 'Linux', 'cros' => 'ChromeOS', 'bsd' => 'BSD'] as $p => $name) {
            if (str_starts_with($id, $p)) {
                return $name;
            }
        }
        return $id === 'Unknown' || $id === 'unknown' ? 'Unknown' : $id;
    }
}
