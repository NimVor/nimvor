<?php

declare(strict_types=1);

namespace NimVorSite\Repositories;

use NimVorSite\Core\Db;

/** Page visits per day and route. No address, cookie or other identifier is stored. */
final class Visits
{
    public function __construct(private readonly Db $db)
    {
    }

    public function record(string $route): void
    {
        $this->db->run('INSERT INTO visits (day, route, hits) VALUES (UTC_DATE(), ?, 1) ON DUPLICATE KEY UPDATE hits = hits + 1', [substr($route, 0, 120)]);
    }

    /** @return list<array{route: string, hits: int}> */
    public function top(int $days, int $limit = 10): array
    {
        $rows = $this->db->all('SELECT route, SUM(hits) AS hits FROM visits WHERE day > DATE_SUB(UTC_DATE(), INTERVAL ? DAY) GROUP BY route ORDER BY hits DESC LIMIT ' . (int) $limit, [$days]);
        return array_map(static fn (array $r): array => ['route' => (string) $r['route'], 'hits' => (int) $r['hits']], $rows);
    }

    /** @return array<string, int> day => visits for the last $days days, oldest first, zeros included */
    public function perDay(int $days): array
    {
        $rows = $this->db->all('SELECT day, SUM(hits) AS hits FROM visits WHERE day > DATE_SUB(UTC_DATE(), INTERVAL ? DAY) GROUP BY day', [$days]);
        return Series::fill(array_column($rows, 'hits', 'day'), $days);
    }
}
