<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Repositories\UserRepository;

/**
 * FFmpeg per hosting plan. The server's FFmpeg programs can only be run by
 * the whacp-ffmpeg group; its members are the system accounts of websites
 * whose owner may use FFmpeg: administrators' websites, and accounts whose
 * plan (and, for a reseller's customer, the reseller's plan too) includes
 * it. Suspended accounts never do.
 */
final class FfmpegService
{
    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
        private readonly WebsiteService $websites,
        private readonly AuditLogger $audit,
    ) {
    }

    /** @return list<string> system accounts allowed on this server */
    public function allowedUsers(): array
    {
        $rows = $this->db->fetchAll(
            'SELECT DISTINCT w.system_user FROM websites w
             JOIN users u ON u.id = w.owner_id
             JOIN roles ro ON ro.id = u.role_id
             LEFT JOIN plans p ON p.id = u.plan_id
             LEFT JOIN users r ON r.id = u.parent_id
             LEFT JOIN roles rr ON rr.id = r.role_id
             LEFT JOIN plans rp ON rp.id = r.plan_id
             WHERE w.server_id = ? AND w.system_user <> \'\' AND NOT ' . UserRepository::SUSPENDED . '
               AND (ro.scope = \'all\'
                    OR (COALESCE(p.ffmpeg, 0) = 1 AND (r.id IS NULL OR rr.scope = \'all\' OR COALESCE(rp.ffmpeg, 0) = 1)))
             ORDER BY w.system_user',
            [$this->server->id()]
        );
        $users = array_values(array_unique(array_map(static fn (array $r): string => (string) $r['system_user'], $rows)));
        // An account shared with a website whose owner may not use FFmpeg
        // is left out: access follows the most restricted owner.
        $denied = array_column($this->db->fetchAll(
            'SELECT DISTINCT w.system_user FROM websites w WHERE w.server_id = ? AND w.system_user IN (' . ($users === [] ? "''" : implode(',', array_fill(0, count($users), '?'))) . ')
             AND w.owner_id NOT IN (SELECT u.id FROM users u JOIN roles ro ON ro.id = u.role_id LEFT JOIN plans p ON p.id = u.plan_id LEFT JOIN users r ON r.id = u.parent_id LEFT JOIN roles rr ON rr.id = r.role_id LEFT JOIN plans rp ON rp.id = r.plan_id
                 WHERE NOT ' . UserRepository::SUSPENDED . ' AND (ro.scope = \'all\' OR (COALESCE(p.ffmpeg, 0) = 1 AND (r.id IS NULL OR rr.scope = \'all\' OR COALESCE(rp.ffmpeg, 0) = 1))))',
            array_merge([$this->server->id()], $users)
        ), 'system_user');
        return array_values(array_diff($users, $denied));
    }

    /** @return array<string, mixed> */
    public function status(): array
    {
        return (array) json_decode((string) json_encode($this->agent->call('ffmpeg.status', [], 60)), true);
    }

    /**
     * Bring the group's members in line with the plans (when FFmpeg is
     * installed). Accounts that lost access get their PHP reloaded, so no
     * running PHP process keeps the group.
     *
     * @return list<string> log lines
     */
    public function reconcile(bool $force = false): array
    {
        $status = $this->status();
        if (empty($status['installed'])) {
            return [];
        }
        $users = $this->allowedUsers();
        $members = array_map('strval', (array) ($status['members'] ?? []));
        sort($members);
        if (!$force && !empty($status['restricted']) && $members === $users) {
            return [];
        }
        $res = (array) $this->agent->call('ffmpeg.access', ['users' => $users], 120);
        $removed = array_map('strval', (array) ($res['removed'] ?? []));
        $log = [];
        if ($removed !== []) {
            $versions = [];
            foreach ($this->db->fetchAll('SELECT * FROM websites WHERE server_id = ? AND php_version <> \'\' AND system_user IN (' . implode(',', array_fill(0, count($removed), '?')) . ')', array_merge([$this->server->id()], $removed)) as $site) {
                $versions[(string) $site['php_version']] ??= $site;
            }
            foreach ($versions as $v => $site) {
                try {
                    $this->websites->restartPhp($site);
                } catch (AgentException $e) {
                    $log[] = "FFmpeg: PHP {$v} was not reloaded: " . $e->getMessage();
                }
            }
        }
        $added = array_map('strval', (array) ($res['added'] ?? []));
        if ($added !== [] || $removed !== []) {
            $this->audit->record('ffmpeg.access', 'server', 'ffmpeg', AuditLogger::SUCCESS, ['added' => $added, 'removed' => $removed]);
            $log[] = 'FFmpeg access: ' . count($added) . ' account(s) added, ' . count($removed) . ' removed.';
        }
        return $log;
    }
}
