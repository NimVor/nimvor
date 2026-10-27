<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Core\Config;
use NimVor\Database\Connection;

/**
 * Keeps history tables bounded (run by the scheduler):
 *   - settled background jobs: 90 days;
 *   - finished deployments: 90 days, always the latest 20 per website;
 *   - audit log: audit.retention_days (default 365; 0 keeps it forever).
 * Webhook deliveries are kept: forgetting one would let it be replayed.
 */
final class RetentionService
{
    public const JOB_DAYS = 90;
    public const DEPLOY_DAYS = 90;
    public const DEPLOY_KEEP = 20;
    public const AUDIT_DAYS = 365;

    public function __construct(
        private readonly Connection $db,
        private readonly Config $config,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @return array{jobs: int, deployments: int, audit: int} rows removed */
    public function prune(?int $now = null): array
    {
        $now ??= time();
        $jobs = $this->db->run(
            'DELETE FROM agent_jobs WHERE settled_at IS NOT NULL AND settled_at < ?',
            [gmdate('c', $now - self::JOB_DAYS * 86400)]
        )->rowCount();
        $deployments = $this->db->run(
            "DELETE FROM deployments WHERE status != 'running' AND created_at < ? AND id NOT IN (
                SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY website_id ORDER BY id DESC) AS rn FROM deployments) WHERE rn <= ?
            )",
            [gmdate('c', $now - self::DEPLOY_DAYS * 86400), self::DEPLOY_KEEP]
        )->rowCount();
        $audit = 0;
        $days = (int) $this->config->get('audit.retention_days', self::AUDIT_DAYS);
        if ($days > 0) {
            $before = gmdate('c', $now - $days * 86400);
            $audit = $this->db->run('DELETE FROM audit_logs WHERE created_at < ?', [$before])->rowCount();
            if ($audit > 0) {
                // The removal itself stays on record.
                $this->audit->record('audit.pruned', 'audit_log', '', AuditLogger::SUCCESS, ['removed' => $audit, 'before' => $before, 'retention_days' => $days]);
            }
        }
        return ['jobs' => $jobs, 'deployments' => $deployments, 'audit' => $audit];
    }
}
