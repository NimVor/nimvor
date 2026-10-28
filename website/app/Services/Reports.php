<?php

declare(strict_types=1);

namespace NimVorSite\Services;

use NimVorSite\Cache\Cache;
use NimVorSite\Core\Db;
use NimVorSite\Repositories\Series;
use NimVorSite\Repositories\Visits;

/** The administrator's numbers. Cached for a minute: they are a summary, not a ledger. */
final class Reports
{
    public function __construct(private readonly Db $db, private readonly Cache $cache, private readonly Visits $visits)
    {
    }

    /** @return array<string, mixed> */
    public function overview(): array
    {
        $cached = $this->cache->get('report:overview');
        if (is_array($cached)) {
            return $cached;
        }
        $signups = $this->db->all('SELECT DATE(created_at) AS d, COUNT(*) AS n FROM users WHERE created_at > DATE_SUB(UTC_DATE(), INTERVAL 30 DAY) GROUP BY d');
        $report = [
            'generated_at' => gmdate('Y-m-d H:i:s'),
            'totals' => [
                'users' => (int) $this->db->scalar('SELECT COUNT(*) FROM users'),
                'clients' => (int) $this->db->scalar("SELECT COUNT(*) FROM users WHERE role = 'client'"),
                'admins' => (int) $this->db->scalar("SELECT COUNT(*) FROM users WHERE role = 'admin'"),
                'posts' => (int) $this->db->scalar("SELECT COUNT(*) FROM posts WHERE status = 'published'"),
                'sponsors' => (int) $this->db->scalar('SELECT COUNT(*) FROM sponsors'),
            ],
            'signups' => Series::fill(array_column($signups, 'n', 'd'), 30),
            'messages' => $this->byStatus('contact_messages', ['new', 'read', 'closed']),
            'requests' => $this->byStatus('build_requests', ['new', 'quoted', 'in_progress', 'done', 'closed']),
            'visits' => $this->visits->perDay(14),
            'top_pages' => $this->visits->top(14),
        ];
        $this->cache->set('report:overview', $report, 60);
        return $report;
    }

    /**
     * @param list<string> $statuses
     * @return array<string, int>
     */
    private function byStatus(string $table, array $statuses): array
    {
        $rows = array_column($this->db->all("SELECT status, COUNT(*) AS n FROM {$table} GROUP BY status"), 'n', 'status');
        $out = [];
        foreach ($statuses as $s) {
            $out[$s] = (int) ($rows[$s] ?? 0);
        }
        return $out;
    }

    /**
     * Rows for a CSV download. Cells that a spreadsheet could read as a formula are
     * prefixed with an apostrophe.
     *
     * @return list<list<string>>
     */
    public function csv(string $type): array
    {
        [$head, $rows] = match ($type) {
            'users' => [['id', 'email', 'name', 'role', 'active', 'created_at', 'last_login_at'], $this->db->all('SELECT id, email, name, role, is_active, created_at, last_login_at FROM users ORDER BY id')],
            'requests' => [['id', 'name', 'email', 'service', 'status', 'created_at'], $this->db->all('SELECT id, name, email, service, status, created_at FROM build_requests ORDER BY id')],
            'messages' => [['id', 'name', 'email', 'subject', 'status', 'created_at'], $this->db->all('SELECT id, name, email, subject, status, created_at FROM contact_messages ORDER BY id')],
            default => throw new \InvalidArgumentException('unknown report'),
        };
        $safe = static fn (mixed $v): string => preg_match('/^[=+\-@\t\r]/', (string) $v) === 1 ? "'" . $v : (string) $v;
        $out = [$head];
        foreach ($rows as $r) {
            $out[] = array_map($safe, array_values($r));
        }
        return $out;
    }
}
