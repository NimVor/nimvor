<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentException;
use NimVor\Database\Connection;

/**
 * Numbers for the administrator dashboard's widgets, from the panel's own
 * records and one quick agent call. Each method fails soft: a widget with no
 * data shows an empty state, it never breaks the dashboard.
 */
final class DashboardWidgetsService
{
    public function __construct(
        private readonly Connection $db,
        private readonly ServerDiskService $disk,
        private readonly ServerContext $server,
    ) {
    }

    /**
     * Backups: the last completed one, failures in the last 7 days, runs now.
     *
     * @return array{last: ?string, failed: int, running: int, completed: int, schedules: int}
     */
    public function backups(): array
    {
        $sid = $this->server->id();
        $week = gmdate('c', time() - 7 * 86400);
        return [
            'last' => ($v = $this->db->scalar("SELECT MAX(created_at) FROM backups WHERE server_id = ? AND status = 'completed'", [$sid])) === null ? null : (string) $v,
            'failed' => (int) $this->db->scalar("SELECT COUNT(*) FROM backups WHERE server_id = ? AND status = 'failed' AND created_at >= ?", [$sid, $week]),
            'running' => (int) $this->db->scalar("SELECT COUNT(*) FROM backups WHERE server_id = ? AND status = 'running'", [$sid]),
            'completed' => (int) $this->db->scalar("SELECT COUNT(*) FROM backups WHERE server_id = ? AND status = 'completed'", [$sid]),
            'schedules' => (int) $this->db->scalar('SELECT COUNT(*) FROM backup_schedules WHERE server_id = ?', [$sid]),
        ];
    }

    /**
     * Disk: the filesystems now and the biggest places from the latest scan.
     *
     * @return array{filesystems: list<array<string, mixed>>, areas: list<array<string, mixed>>, scanned_at: ?string}
     */
    public function disk(): array
    {
        try {
            $fs = $this->disk->filesystems();
        } catch (AgentException) {
            $fs = [];
        }
        $scan = $this->disk->latest();
        $areas = array_values(array_filter((array) ($scan['result']['areas'] ?? []), static fn ($a): bool => empty(((array) $a)['missing'])));
        usort($areas, static fn ($a, $b): int => (int) ((array) $b)['bytes'] <=> (int) ((array) $a)['bytes']);
        return ['filesystems' => $fs, 'areas' => array_slice(array_map(static fn ($a): array => (array) $a, $areas), 0, 4), 'scanned_at' => $scan === null ? null : (string) ($scan['result']['scanned_at'] ?? $scan['finished_at'] ?? '')];
    }
}
