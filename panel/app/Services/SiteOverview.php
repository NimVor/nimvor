<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Database\Connection;
use WhaCP\Repositories\DatabaseRepository;

/**
 * Read-only summary of a website for its dashboard: counts and states from
 * the panel's own records (no agent calls), each scoped like the page it
 * links to.
 */
final class SiteOverview
{
    public function __construct(
        private readonly Connection $db,
        private readonly DatabaseRepository $databases,
        private readonly MailService $mail,
        private readonly DnsService $dns,
        private readonly ServerContext $server,
    ) {
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $user
     * @return array<string, mixed>
     */
    public function summary(array $site, array $user): array
    {
        $id = (int) $site['id'];
        $out = [
            'databases' => count($this->databases->databases($this->server->id(), $user, $id)),
            'mail' => null,
            'dns' => null,
            'cron' => (int) $this->db->scalar('SELECT COUNT(*) FROM cron_jobs WHERE website_id = ?', [$id]),
            'workers' => (int) $this->db->scalar('SELECT COUNT(*) FROM workers WHERE website_id = ?', [$id]),
            'git' => $this->db->fetch('SELECT repo, branch FROM git_repositories WHERE website_id = ?', [$id]),
            'backups' => (int) $this->db->scalar('SELECT COUNT(*) FROM backups WHERE website_id = ?', [$id]),
            'last_backup' => $this->db->fetch("SELECT created_at, status FROM backups WHERE website_id = ? AND status = 'completed' ORDER BY id DESC LIMIT 1", [$id]),
            'backup_schedules' => (int) $this->db->scalar('SELECT COUNT(*) FROM backup_schedules WHERE website_id = ?', [$id]),
            'apps' => array_values(array_unique(array_column($this->db->fetchAll("SELECT app FROM app_installs WHERE website_id = ? AND status = 'succeeded' ORDER BY id", [$id]), 'app'))),
        ];
        $d = $this->mail->forWebsite($id);
        if ($d !== null && $this->mail->domain((int) $d['id'], $user) !== null) {
            $out['mail'] = [
                'id' => (int) $d['id'],
                'mailboxes' => count($this->mail->mailboxes((int) $d['id'])),
                'aliases' => count($this->mail->aliases((int) $d['id'])),
            ];
        }
        $z = $this->dns->forWebsite($id);
        if ($z !== null && $this->dns->zone((int) $z['id'], $user) !== null) {
            $out['dns'] = ['id' => (int) $z['id'], 'records' => count($this->dns->records((int) $z['id']))];
        }
        return $out;
    }
}
