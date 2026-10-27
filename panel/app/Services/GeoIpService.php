<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Database\Connection;
use WhaCP\Support\UserFacingException;

/**
 * IP address → country, from DB-IP's "IP to Country Lite" database
 * (CC BY 4.0, https://db-ip.com), downloaded monthly or uploaded by an
 * administrator. Addresses are kept as 16 bytes (IPv4 mapped into IPv6),
 * so one ordered index serves both families.
 */
final class GeoIpService
{
    public const SOURCE = 'https://download.db-ip.com/free/dbip-country-lite-%s.csv.gz';
    private const MAX_BYTES = 64_000_000;

    public function __construct(
        private readonly Connection $db,
        private readonly SettingsService $settings,
        private readonly AuditLogger $audit,
    ) {
    }

    /** 16-byte form of an address, or null. */
    public static function bin(string $ip): ?string
    {
        $b = @inet_pton($ip);
        if ($b === false) {
            return null;
        }
        return strlen($b) === 4 ? str_repeat("\0", 10) . "\xff\xff" . $b : $b;
    }

    /** The printable address of a 16-byte form (IPv4 for mapped addresses). */
    public static function ip(string $bin): string
    {
        if (str_starts_with($bin, str_repeat("\0", 10) . "\xff\xff")) {
            return (string) inet_ntop(substr($bin, 12));
        }
        return (string) inet_ntop($bin);
    }

    public function loaded(): bool
    {
        return $this->db->scalar('SELECT 1 FROM geo_ranges LIMIT 1') !== null;
    }

    /** ISO country code of an address ('' when unknown). */
    public function country(string $ip): string
    {
        $b = self::bin($ip);
        if ($b === null) {
            return '';
        }
        $row = $this->db->fetch('SELECT end, country FROM geo_ranges WHERE start <= ? ORDER BY start DESC LIMIT 1', [$b]);
        return $row !== null && strcmp((string) $row['end'], $b) >= 0 ? (string) $row['country'] : '';
    }

    /**
     * Address ranges of countries (printable, for the firewall).
     *
     * @param list<string> $codes
     * @return list<array{from: string, to: string}>
     */
    public function ranges(array $codes): array
    {
        if ($codes === []) {
            return [];
        }
        $out = [];
        foreach ($this->db->fetchAll('SELECT start, end FROM geo_ranges WHERE country IN (' . implode(',', array_fill(0, count($codes), '?')) . ') ORDER BY start', $codes) as $r) {
            $out[] = ['from' => self::ip((string) $r['start']), 'to' => self::ip((string) $r['end'])];
        }
        return $out;
    }

    /**
     * Replace the database from a DB-IP CSV (optionally gzip-compressed):
     * "first,last,CC" per line. Returns the number of ranges.
     */
    public function import(string $path): int
    {
        $raw = @file_get_contents($path, false, null, 0, self::MAX_BYTES + 1);
        if ($raw === false || $raw === '') {
            throw new UserFacingException('The file could not be read.');
        }
        if (str_starts_with($raw, "\x1f\x8b")) {
            $raw = @gzdecode($raw, self::MAX_BYTES * 4);
            if ($raw === false) {
                throw new UserFacingException('The file is not a valid gzip file.');
            }
        }
        $rows = [];
        foreach (explode("\n", $raw) as $line) {
            $f = str_getcsv(trim($line), ',', '"', '');
            if (count($f) < 3) {
                continue;
            }
            $a = self::bin(trim((string) $f[0]));
            $b = self::bin(trim((string) $f[1]));
            $cc = strtoupper(trim((string) $f[2]));
            if ($a === null || $b === null || strcmp($a, $b) > 0 || !preg_match('/^[A-Z]{2}$/', $cc)) {
                continue;
            }
            $rows[] = [$a, $b, $cc];
        }
        unset($raw);
        if (count($rows) < 1) {
            throw new UserFacingException('The file has no address ranges (expected DB-IP "IP to Country Lite" CSV: first,last,country).');
        }
        $this->db->transaction(function () use ($rows): void {
            $this->db->run('DELETE FROM geo_ranges');
            foreach (array_chunk($rows, 300) as $chunk) {
                $this->db->run('INSERT INTO geo_ranges (start, end, country) VALUES ' . implode(',', array_fill(0, count($chunk), '(?, ?, ?)')), array_merge(...$chunk));
            }
        });
        $this->settings->update(['hulk_geo_updated' => gmdate('Y-m-d')], null);
        $this->audit->record('hulk.geo_import', 'server', 'geoip', AuditLogger::SUCCESS, ['ranges' => count($rows)]);
        return count($rows);
    }

    /**
     * Scheduler pass: download this month's database when the loaded one is
     * from an earlier month (and countries are in use).
     *
     * @param (\Closure(string): ?string)|null $fetch
     */
    public function refresh(?\Closure $fetch = null): ?int
    {
        $month = gmdate('Y-m');
        if (str_starts_with($this->settings->string('hulk_geo_updated'), $month) || $this->settings->string('hulk_geo_tried') === gmdate('Y-m-d')) {
            return null;
        }
        $this->settings->update(['hulk_geo_tried' => gmdate('Y-m-d')], null);
        $fetch ??= static function (string $url): ?string {
            $ch = curl_init($url);
            curl_setopt_array($ch, [CURLOPT_RETURNTRANSFER => true, CURLOPT_TIMEOUT => 120, CURLOPT_CONNECTTIMEOUT => 10, CURLOPT_FOLLOWLOCATION => false,
                CURLOPT_PROTOCOLS => CURLPROTO_HTTPS, CURLOPT_USERAGENT => 'NimVor brute force protection', CURLOPT_MAXFILESIZE => self::MAX_BYTES]);
            $body = curl_exec($ch);
            $code = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
            curl_close($ch);
            return is_string($body) && $code === 200 ? $body : null;
        };
        $body = $fetch(sprintf(self::SOURCE, $month));
        if ($body === null) {
            return null;
        }
        $tmp = tempnam(sys_get_temp_dir(), 'whacp-geo');
        try {
            file_put_contents($tmp, $body);
            return $this->import($tmp);
        } finally {
            @unlink($tmp);
        }
    }
}
