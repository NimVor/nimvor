<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Database\Connection;
use NimVor\Repositories\UserRepository;
use NimVor\Repositories\WebsiteRepository;

/**
 * Enforces the plans' monthly bandwidth (data transfer).
 *
 * An account whose websites (a reseller: with its customers' websites) sent
 * more than its plan allows this month is marked for the month; its
 * websites then answer "509 Bandwidth limit exceeded" (certificate renewals
 * still work). The mark ends when the month ends, the limit is raised or
 * removed, or an administrator resets the month's counter. Administrators
 * are never limited.
 *
 * reconcile() runs after every bandwidth measurement (scheduler) and after
 * plan, limit and counter changes. It re-applies exactly the websites whose
 * state on this server differs from their account's, so a failed apply or
 * another server's websites are caught up on the next pass.
 */
final class BandwidthLimitService
{
    public function __construct(
        private readonly Connection $db,
        private readonly PlanLimits $limits,
        private readonly WebsiteRepository $sites,
        private readonly WebsiteService $websites,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
        private readonly ?NotificationService $notifications = null,
    ) {
    }

    /** @return list<string> what changed */
    public function reconcile(): array
    {
        $month = gmdate('Y-m');
        $lines = [];
        $accounts = $this->db->fetchAll("SELECT u.id, u.username, u.bw_blocked_month, r.scope FROM users u JOIN roles r ON r.id = u.role_id WHERE u.plan_id IS NOT NULL OR u.bw_blocked_month <> ''");
        foreach ($accounts as $a) {
            $id = (int) $a['id'];
            $limit = $a['scope'] === 'all' ? 0 : $this->limits->value($id, 'bandwidth_mb');
            $used = $limit > 0 ? $this->limits->count('bandwidth_mb', $this->limits->owners($id)) : 0;
            $over = $limit > 0 && $used > $limit;
            $marked = (string) $a['bw_blocked_month'];
            if ($over && $marked !== $month) {
                $this->db->run('UPDATE users SET bw_blocked_month = ? WHERE id = ?', [$month, $id]);
                $this->audit->record('account.bandwidth_block', 'user', (string) $id, AuditLogger::SUCCESS, ['username' => $a['username'], 'month' => $month, 'used_mb' => $used, 'limit_mb' => $limit]);
                $this->notifications?->notify('bandwidth-block:' . $id . ':' . $month, 'bandwidth', 'warning', 'Monthly bandwidth used up: ' . $a['username'], [
                    'owner_id' => $id,
                    'body' => sprintf('%s sent %s MB of the %s MB its plan allows in %s. Its websites answer "509 Bandwidth limit exceeded" until the month ends, the plan allows more or the counter is reset.', $a['username'], number_format($used), number_format($limit), gmdate('F Y')),
                ]);
                $lines[] = $a['username'] . ': over its monthly bandwidth (' . $used . ' of ' . $limit . ' MB); websites blocked';
            } elseif (!$over && $marked !== '') {
                $this->db->run("UPDATE users SET bw_blocked_month = '' WHERE id = ?", [$id]);
                if ($marked === $month) {
                    $this->audit->record('account.bandwidth_unblock', 'user', (string) $id, AuditLogger::SUCCESS, ['username' => $a['username'], 'month' => $month, 'used_mb' => $used, 'limit_mb' => $limit]);
                }
                $lines[] = $a['username'] . ': within its monthly bandwidth again; websites unblocked';
            }
        }
        // The websites on this server whose state differs.
        $stale = $this->db->fetchAll(
            'SELECT w.id FROM websites w LEFT JOIN users u ON u.id = w.owner_id WHERE w.server_id = ? AND w.bandwidth_applied <> (CASE WHEN u.id IS NOT NULL AND ' . UserRepository::BANDWIDTH_BLOCKED . ' THEN 1 ELSE 0 END)',
            [$this->server->id()]
        );
        foreach ($stale as $row) {
            $site = $this->sites->find($this->server->id(), (int) $row['id'], null);
            if ($site === null) {
                continue;
            }
            try {
                $this->websites->applyState($site);
            } catch (\Throwable $e) {
                $lines[] = $site['domain'] . ': not applied: ' . $e->getMessage();
            }
        }
        return $lines;
    }
}
