<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Database\Connection;
use NimVor\Repositories\WebsiteRepository;

/**
 * What needs attention across the websites a user manages: for each
 * website its problems (switched off, no HTTPS, no recent backup, malware
 * found, old PHP, email DNS records missing or wrong), worst first. Reads the
 * panel's own records only (one agent call for the mail host name), so it is
 * cheap enough for the dashboard.
 */
final class SitesHealthService
{
    /** Backups older than this many days count as missing. */
    public const BACKUP_DAYS = 7;

    /** Issue categories: label and the route that fixes them (a website page, or a server page). */
    public const CATEGORIES = [
        'site' => ['Switched off', 'sites.php'],
        'https' => ['HTTPS', 'sites.ssl'],
        'backup' => ['Backups', 'sites.backups'],
        'malware' => ['Malware', 'sites.malware'],
        'php' => ['PHP version', 'sites.php'],
        'email' => ['Email DNS (SPF, DKIM, DMARC)', 'mail'],
    ];

    public function __construct(
        private readonly Connection $db,
        private readonly WebsiteRepository $sites,
        private readonly MailService $mail,
        private readonly DnsService $dns,
        private readonly ServerContext $server,
    ) {
    }

    /**
     * @param array<string, mixed> $user
     * @return array{
     *   sites: list<array{site: array<string, mixed>, issues: list<array<string, mixed>>, worst: string, mail_domain_id: int}>,
     *   counts: array<string, int>, total: int, healthy: int, mail_host: string
     * }
     */
    public function overview(array $user): array
    {
        $sites = $this->sites->all($this->server->id(), $user);
        $ids = array_map(static fn (array $s): int => (int) $s['id'], $sites);
        $lastBackup = $this->lastBackups($ids);
        $infected = $this->infected($ids);
        $status = $this->mail->status();
        $mailHost = strtolower((string) (($status ?? [])['hostname'] ?? ''));
        $mailIssues = $status !== null && !empty($status['configured']) ? $this->emailIssues($user, $mailHost) : [];

        $out = [];
        $counts = array_fill_keys(array_keys(self::CATEGORIES), 0);
        $healthy = 0;
        $cutoff = gmdate('c', time() - self::BACKUP_DAYS * 86400);
        foreach ($sites as $s) {
            $id = (int) $s['id'];
            $issues = [];
            if (empty($s['enabled'])) {
                $issues[] = $this->issue('site', 'bad', 'The website is switched off', 'It is not served to visitors.');
            }
            if (empty($s['https'])) {
                $issues[] = $this->issue('https', 'warn', 'No HTTPS', 'Visitors reach the site over an unencrypted connection.');
            } elseif (empty($s['force_https'])) {
                $issues[] = $this->issue('https', 'info', 'Plain http:// still works', 'Turn on Force HTTPS so visitors are redirected.');
            }
            if (!isset($lastBackup[$id])) {
                $issues[] = $this->issue('backup', 'warn', 'No backup yet', 'There is nothing to restore from if something goes wrong.');
            } elseif ($lastBackup[$id] < $cutoff) {
                $issues[] = $this->issue('backup', 'warn', 'Last backup is over ' . self::BACKUP_DAYS . ' days old', 'Latest: ' . substr($lastBackup[$id], 0, 10) . '.');
            }
            if (($infected[$id] ?? 0) > 0) {
                $issues[] = $this->issue('malware', 'bad', 'Malware found', $infected[$id] . ' infected file(s) in the last scan.');
            }
            if (($s['kind'] ?? '') === 'php' && (string) ($s['php_version'] ?? '') !== '' && version_compare((string) $s['php_version'], '8.1', '<')) {
                $issues[] = $this->issue('php', 'warn', 'PHP ' . $s['php_version'] . ' no longer gets security updates', 'Choose PHP 8.1 or newer.');
            }
            $mailDomainId = 0;
            foreach ($mailIssues[$id] ?? [] as $m) {
                $issues[] = $m + ['category' => 'email'];
                $mailDomainId = (int) $m['mail_domain_id'];
            }
            foreach ($issues as $i) {
                if ($i['level'] !== 'info') {
                    $counts[$i['category']]++;
                }
            }
            $actionable = array_filter($issues, static fn (array $i): bool => $i['level'] !== 'info');
            if ($actionable === []) {
                $healthy++;
            }
            usort($issues, static fn (array $a, array $b): int => self::rank($a['level']) <=> self::rank($b['level']));
            $out[] = ['site' => $s, 'issues' => $issues, 'worst' => $issues[0]['level'] ?? 'ok', 'mail_domain_id' => $mailDomainId];
        }
        usort($out, static fn (array $a, array $b): int => [self::rank($a['worst']), $a['site']['domain']] <=> [self::rank($b['worst']), $b['site']['domain']]);
        return ['sites' => $out, 'counts' => $counts, 'total' => count($sites), 'healthy' => $healthy, 'mail_host' => $mailHost];
    }

    /**
     * Email DNS problems per website id, for domains whose zone this panel hosts.
     *
     * @param array<string, mixed> $user
     * @return array<int, list<array<string, mixed>>> website id => issues, each with the mail domain's id
     */
    private function emailIssues(array $user, string $mailHost): array
    {
        $zones = [];
        foreach ($this->dns->zones($user) as $z) {
            $zones[strtolower((string) $z['name'])] = (int) $z['id'];
        }
        $out = [];
        foreach ($this->mail->domains($user) as $d) {
            $wid = (int) ($d['website_id'] ?? 0);
            $zone = $zones[strtolower((string) $d['domain'])] ?? null;
            if ($wid === 0 || $zone === null) {
                continue; // DNS is hosted elsewhere: the records cannot be read from here
            }
            foreach (EmailHealth::check($d, $this->dns->records($zone), $mailHost) as $issue) {
                $out[$wid][] = $issue + ['mail_domain_id' => (int) $d['id']];
            }
        }
        return $out;
    }

    /**
     * Add the email records that are missing from a domain's zone on this panel.
     *
     * @param array<string, mixed> $user
     * @return int records added
     */
    public function fixEmail(int $mailDomainId, array $user, string $mailHost): int
    {
        $domain = $this->mail->domain($mailDomainId, $user);
        if ($domain === null) {
            return 0;
        }
        return $this->mail->publishDns($domain, $mailHost, $user);
    }

    /** @param list<int> $ids @return array<int, string> website id => time of its latest completed backup */
    private function lastBackups(array $ids): array
    {
        if ($ids === []) {
            return [];
        }
        $in = implode(',', array_fill(0, count($ids), '?'));
        $out = [];
        foreach ($this->db->fetchAll("SELECT website_id, MAX(created_at) AS at FROM backups WHERE status = 'completed' AND website_id IN ({$in}) GROUP BY website_id", $ids) as $r) {
            $out[(int) $r['website_id']] = (string) $r['at'];
        }
        return $out;
    }

    /** @param list<int> $ids @return array<int, int> website id => infected files in its latest finished scan */
    private function infected(array $ids): array
    {
        if ($ids === []) {
            return [];
        }
        $in = implode(',', array_fill(0, count($ids), '?'));
        $out = [];
        foreach ($this->db->fetchAll("SELECT a.website_id, a.infected FROM av_scans a WHERE a.website_id IN ({$in}) AND a.status <> 'running' AND a.id = (SELECT MAX(b.id) FROM av_scans b WHERE b.website_id = a.website_id AND b.status <> 'running')", $ids) as $r) {
            $out[(int) $r['website_id']] = (int) $r['infected'];
        }
        return $out;
    }

    /** @return array{category: string, level: string, title: string, detail: string} */
    private function issue(string $category, string $level, string $title, string $detail): array
    {
        return ['category' => $category, 'level' => $level, 'title' => $title, 'detail' => $detail];
    }

    private static function rank(string $level): int
    {
        return ['bad' => 0, 'warn' => 1, 'info' => 2, 'ok' => 3][$level] ?? 3;
    }
}
