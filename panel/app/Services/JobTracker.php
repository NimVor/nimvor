<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Database\Connection;

/**
 * Remembers which panel user started which agent job, so job logs are
 * only shown to people entitled to see them.
 */
final class JobTracker
{
    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
        private readonly DiscoveryService $discovery,
    ) {
    }

    public function track(string $jobId, string $kind, string $targetType, string $targetId, ?int $userId): void
    {
        if (!preg_match('/^[a-f0-9]{16}$/', $jobId)) {
            return;
        }
        $this->db->run(
            'INSERT OR IGNORE INTO agent_jobs (server_id, job_id, kind, target_type, target_id, user_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)',
            [$this->server->id(), $jobId, $kind, $targetType, $targetId, $userId, gmdate('c')]
        );
    }

    /** @return array<string, mixed>|null */
    public function find(string $jobId): ?array
    {
        return $this->db->fetch('SELECT * FROM agent_jobs WHERE server_id = ? AND job_id = ?', [$this->server->id(), $jobId]);
    }

    /** @return list<array<string, mixed>> */
    public function recentFor(string $targetType, string $targetId, int $limit = 10): array
    {
        return $this->db->fetchAll(
            'SELECT j.*, u.username FROM agent_jobs j LEFT JOIN users u ON u.id = j.user_id WHERE j.server_id = ? AND j.target_type = ? AND j.target_id = ? ORDER BY j.id DESC LIMIT ?',
            [$this->server->id(), $targetType, $targetId, $limit]
        );
    }

    /**
     * Log since offset, plus status; updates the stored status.
     *
     * @return array<string, mixed>
     */
    public function poll(string $jobId, int $offset): array
    {
        $res = (array) $this->agent->call('jobs.log', ['id' => $jobId, 'offset' => max(0, $offset)]);
        $status = (string) ($res['job']['status'] ?? '');
        if (!empty($res['done'])) {
            $this->discovery->invalidate();
        }
        if ($status !== '') {
            $this->db->run('UPDATE agent_jobs SET status = ? WHERE server_id = ? AND job_id = ?', [$status, $this->server->id(), $jobId]);
        }
        return $res;
    }
}
