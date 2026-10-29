<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

use NimVorSite\Core\Db;

/** Anonymous installation reports. A report is a random id, the version and the system's type: nothing else. */
final class Installs
{
    public function __construct(private readonly Db $db)
    {
    }

    public function record(string $id, string $version, string $os, string $osVersion, string $arch): void
    {
        $this->db->run(
            'INSERT INTO installs (install_id, first_seen, last_seen, version, os, os_version, arch) VALUES (?, UTC_TIMESTAMP(), UTC_TIMESTAMP(), ?, ?, ?, ?)
             ON DUPLICATE KEY UPDATE last_seen = UTC_TIMESTAMP(), version = VALUES(version), os = VALUES(os), os_version = VALUES(os_version), arch = VALUES(arch), pings = pings + 1',
            [$id, $version, $os, $osVersion, $arch]
        );
    }

    /** @return array{total: int, active_30: int, new_30: int, latest: ?string} */
    public function totals(): array
    {
        $r = $this->db->one('SELECT COUNT(*) AS total, SUM(last_seen > DATE_SUB(UTC_TIMESTAMP(), INTERVAL 30 DAY)) AS active, SUM(first_seen > DATE_SUB(UTC_TIMESTAMP(), INTERVAL 30 DAY)) AS fresh, MAX(first_seen) AS latest FROM installs') ?? [];
        return ['total' => (int) ($r['total'] ?? 0), 'active_30' => (int) ($r['active'] ?? 0), 'new_30' => (int) ($r['fresh'] ?? 0), 'latest' => isset($r['latest']) ? (string) $r['latest'] : null];
    }

    /**
     * @param 'version'|'os'|'arch' $column
     * @return array<string, int> value => servers, most first
     */
    public function breakdown(string $column): array
    {
        if (!in_array($column, ['version', 'os', 'arch'], true)) {
            throw new \InvalidArgumentException('unknown column');
        }
        $label = $column === 'os' ? "CONCAT(os, ' ', os_version)" : $column;
        $rows = $this->db->all("SELECT {$label} AS k, COUNT(*) AS n FROM installs GROUP BY k ORDER BY n DESC, k DESC LIMIT 12");
        return array_map('intval', array_column($rows, 'n', 'k'));
    }

    /** @return array<string, int> Y-m => new servers, the last $months months, oldest first, zeros included */
    public function perMonth(int $months): array
    {
        $rows = $this->db->all("SELECT DATE_FORMAT(first_seen, '%Y-%m') AS m, COUNT(*) AS n FROM installs WHERE first_seen >= DATE_SUB(DATE_FORMAT(UTC_DATE(), '%Y-%m-01'), INTERVAL ? MONTH) GROUP BY m", [$months - 1]);
        $by = array_column($rows, 'n', 'm');
        $out = [];
        for ($i = $months - 1; $i >= 0; $i--) {
            $m = gmdate('Y-m', (int) strtotime('first day of -' . $i . ' month 00:00:00 UTC'));
            $out[$m] = (int) ($by[$m] ?? 0);
        }
        return $out;
    }
}
