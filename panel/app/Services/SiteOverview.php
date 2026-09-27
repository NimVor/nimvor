<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
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
        private readonly DatabaseService $dbService,
        private readonly SettingsService $settings,
        private readonly AgentClient $agent,
    ) {
    }

    /**
     * The address the website answers on: the DNS default IPv4 when set,
     * otherwise the server's first global IPv4 address.
     */
    public function serverIp(): ?string
    {
        $ip = trim($this->settings->string('dns_default_ipv4'));
        if (filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) !== false) {
            return $ip;
        }
        try {
            $info = (array) $this->agent->call('system.info');
        } catch (AgentException) {
            return null;
        }
        foreach ((array) (((array) ($info['system'] ?? []))['interfaces'] ?? []) as $if) {
            foreach ((array) (((array) $if)['addresses'] ?? []) as $a) {
                $a = explode('/', (string) $a)[0];
                if (filter_var($a, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4 | FILTER_FLAG_NO_RES_RANGE) !== false && !str_starts_with($a, '127.')) {
                    return $a;
                }
            }
        }
        return null;
    }

    /**
     * Size of the website's databases, or null when the database server
     * cannot be asked.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $user
     */
    public function databaseBytes(array $site, array $user): ?int
    {
        $names = array_column($this->databases->databases($this->server->id(), $user, (int) $site['id']), 'name');
        if ($names === []) {
            return 0;
        }
        $live = $this->dbService->live();
        if ($live === null) {
            return null;
        }
        $total = 0;
        foreach ((array) ($live['databases'] ?? []) as $d) {
            $d = (array) $d;
            if (in_array((string) ($d['name'] ?? ''), $names, true)) {
                $total += (int) ($d['size_bytes'] ?? 0);
            }
        }
        return $total;
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
            'subdomains' => (int) $this->db->scalar("SELECT COUNT(*) FROM websites WHERE parent_id = ? AND domain LIKE ?", [$id, '%.' . $site['domain']]),
            'addons' => (int) $this->db->scalar("SELECT COUNT(*) FROM websites WHERE parent_id = ? AND domain NOT LIKE ?", [$id, '%.' . $site['domain']]),
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
