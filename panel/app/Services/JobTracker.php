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
        private readonly NotificationService $notifications,
    ) {
    }

    /**
     * Jobs whose success is not worth a notification (quick, and started by
     * someone who watches them); their failures always are.
     */
    private const QUIET_ON_SUCCESS = ['Cron: ', 'Compress in ', 'Extract in ', 'Malware scan of '];

    /** How long an unfinished job is followed before it is given up on. */
    private const SETTLE_WINDOW = 2 * 86400;

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
    /**
     * Whether a user may follow a job: "all"-scope roles see every job,
     * others their own and (resellers) their customers' jobs.
     *
     * @param array<string, mixed> $job
     * @param array<string, mixed> $user
     */
    public function visibleTo(array $job, array $user): bool
    {
        if (\WhaCP\Hosting\Scope::isAll($user) || (int) $job['user_id'] === (int) $user['id']) {
            return true;
        }
        if ($job['user_id'] !== null
            && (int) $this->db->scalar('SELECT COUNT(*) FROM users WHERE id = ? AND parent_id = ?', [(int) $job['user_id'], (int) $user['id']]) > 0) {
            return true;
        }
        // Jobs on a website the user may see (e.g. a restore an
        // administrator ran on it), so its notification can show the log.
        if ($job['target_type'] === 'website') {
            [$cond, $params] = \WhaCP\Hosting\Scope::condition($user);
            $params['sid'] = (int) $job['target_id'];
            $params['srv'] = (int) $job['server_id'];
            return (int) $this->db->scalar("SELECT COUNT(*) FROM websites WHERE id = :sid AND server_id = :srv AND {$cond}", $params) > 0;
        }
        return false;
    }

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
        if ($status === 'succeeded' || $status === 'failed') {
            $job = $this->find($jobId);
            if ($job !== null && $job['settled_at'] === null) {
                $this->settle($job, $status, (string) ($res['job']['error'] ?? ''));
            }
        }
        return $res;
    }

    /**
     * Record the outcome of this server's unfinished jobs (scheduler pass),
     * so their results are reported even when nobody watched them.
     *
     * @return list<string> log lines
     */
    public function settlePending(?int $now = null): array
    {
        $now ??= time();
        $log = [];
        $pending = $this->db->fetchAll(
            'SELECT * FROM agent_jobs WHERE server_id = ? AND settled_at IS NULL ORDER BY id LIMIT 200',
            [$this->server->id()]
        );
        foreach ($pending as $job) {
            try {
                $res = (array) $this->agent->call('jobs.get', ['id' => $job['job_id']]);
            } catch (\WhaCP\Agent\AgentException $e) {
                if ($e->errorCode() === 'not_found') {
                    // The agent restarted and forgot it: the outcome is unknown.
                    $this->db->run("UPDATE agent_jobs SET status = 'unknown', settled_at = ? WHERE id = ?", [gmdate('c', $now), (int) $job['id']]);
                } elseif (strtotime((string) $job['created_at']) < $now - self::SETTLE_WINDOW) {
                    $this->db->run('UPDATE agent_jobs SET settled_at = ? WHERE id = ?', [gmdate('c', $now), (int) $job['id']]);
                }
                continue;
            }
            $status = (string) ($res['status'] ?? '');
            if ($status !== 'succeeded' && $status !== 'failed') {
                continue;
            }
            $this->db->run('UPDATE agent_jobs SET status = ? WHERE id = ?', [$status, (int) $job['id']]);
            $this->settle($job, $status, (string) ($res['error'] ?? ''));
            $log[] = "Job {$job['job_id']} ({$job['kind']}): {$status}.";
        }
        return $log;
    }

    /**
     * Record a finished job once and notify about it: always on failure,
     * on success unless it is a quick job.
     *
     * @param array<string, mixed> $job
     */
    private function settle(array $job, string $status, string $error): void
    {
        $done = $this->db->run(
            'UPDATE agent_jobs SET settled_at = ? WHERE id = ? AND settled_at IS NULL',
            [gmdate('c'), (int) $job['id']]
        )->rowCount();
        if ($done === 0) {
            return; // recorded by another request or pass
        }
        $label = (string) $job['kind'];
        $failed = $status === 'failed';
        if (!$failed) {
            foreach (self::QUIET_ON_SUCCESS as $prefix) {
                if (str_starts_with($label, $prefix)) {
                    return;
                }
            }
        }
        $opts = ['job_id' => (string) $job['job_id'], 'server_id' => (int) $job['server_id']];
        if ($job['target_type'] === 'website') {
            $site = $this->db->fetch('SELECT id FROM websites WHERE id = ? AND server_id = ?', [(int) $job['target_id'], (int) $job['server_id']]);
            if ($site === null) {
                return; // the website is gone
            }
            $opts['website_id'] = (int) $site['id'];
            $opts['link'] = '/sites/' . (int) $site['id'];
        } elseif ($job['target_type'] === 'database') {
            $db = $this->db->fetch('SELECT website_id, owner_id FROM databases WHERE id = ?', [(int) $job['target_id']]);
            $opts['website_id'] = $db['website_id'] ?? null;
            $opts['owner_id'] = $db['owner_id'] ?? null;
        }
        if ($failed) {
            $opts['body'] = $error !== '' ? $error : 'The job failed. Open its log for details.';
        }
        $this->notifications->notify(
            'job:' . $job['server_id'] . ':' . $job['job_id'],
            'job',
            $failed ? 'error' : 'success',
            $label . ($failed ? ' failed' : ' finished'),
            $opts
        );
    }
}
