<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Support\UserFacingException;

/**
 * Imports a discovered (non-WhaCP) website into WhaCP management.
 *
 * The agent previews the plan, copies the files into a new WhaCP website
 * and switches Nginx over, restoring the old configuration on any error.
 * The old configuration and files are left in place, and an import can be
 * undone.
 */
final class ImportService
{
    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly WebsiteService $websites,
        private readonly WebsiteRepository $sites,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
        private readonly DiscoveryService $discovery,
    ) {
    }

    /**
     * Spec, database row and names for importing a discovered website.
     *
     * @param array<string, mixed> $found discovery record
     * @param array<string, mixed> $input choices (system_user, php_version)
     * @return array{row: array<string, mixed>, names: list<string>, source: string}
     */
    public function plan(array $found, array $input): array
    {
        $kind = !empty($found['proxy_pass']) ? 'proxy' : (!empty($found['php']) ? 'php' : 'static');
        $php = (string) ($input['php_version'] ?? ($found['php']['version'] ?? ''));
        $available = $this->websites->phpVersions();
        if ($kind === 'php' && !in_array($php, $available, true)) {
            $php = (string) (end($available) ?: '');
        }
        $data = $this->websites->validate([
            'domain' => $found['domain'],
            'aliases' => implode("\n", (array) ($found['aliases'] ?? [])),
            'kind' => $kind,
            'php_version' => $php,
            'proxy_pass' => (string) ($found['proxy_pass'] ?? ''),
            'system_user' => (string) ($input['system_user'] ?? ''),
        ], null);
        $row = $data['row'];
        $cert = (string) ($found['ssl']['cert_file'] ?? '');
        $key = (string) ($found['ssl']['key_file'] ?? '');
        if ($cert !== '' && str_starts_with($cert, '/etc/letsencrypt/live/') && str_starts_with($key, '/etc/letsencrypt/live/')) {
            $row += ['https' => true, 'force_https' => !empty($found['https_redirect']), 'ssl_mode' => 'letsencrypt', 'cert_file' => $cert, 'key_file' => $key];
        }
        return ['row' => $row, 'names' => $data['names'], 'source' => (string) ($found['document_root'] ?? '')];
    }

    /** @param array<string, mixed> $row */
    private static function specFrom(array $row, array $names): array
    {
        return WebsiteService::spec($row + ['aliases' => array_values(array_slice($names, 1)), 'https' => false, 'force_https' => false, 'hsts' => false, 'cert_file' => '', 'key_file' => '', 'enabled' => true], true);
    }

    /**
     * @param array<string, mixed> $plan from plan()
     * @return array<string, mixed> agent preview
     */
    public function preview(array $plan): array
    {
        return (array) $this->agent->call('adopt.preview', ['spec' => self::specFrom($plan['row'], $plan['names']), 'source' => $plan['source']], 120);
    }

    /**
     * Start the import; returns [website id, job id].
     *
     * @param array<string, mixed> $plan
     * @param array<string, mixed> $actor
     * @return array{0: int, 1: string}
     */
    public function run(array $plan, array $actor, ?int $ownerId): array
    {
        $preview = $this->preview($plan);
        if (!empty($preview['blockers'])) {
            throw new UserFacingException('The import is blocked.', implode(' ', (array) $preview['blockers']));
        }
        $row = $plan['row'] + ['server_id' => $this->server->id(), 'owner_id' => $ownerId, 'status' => 'importing'];
        $id = $this->sites->insert($row);
        $this->sites->replaceDomains($this->server->id(), $id, $plan['names']);
        try {
            $res = (array) $this->agent->call('adopt.run', ['spec' => self::specFrom($plan['row'], $plan['names']), 'source' => $plan['source']]);
        } catch (AgentException $e) {
            $this->sites->delete($id);
            throw $e;
        }
        $job = (string) $res['job_id'];
        $this->db->run('INSERT INTO website_imports (website_id, domain, source, database_name, job_id, user_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)', [$id, $row['domain'], $plan['source'], (string) ($preview['database'] ?? ''), $job, (int) $actor['id'], gmdate('c')]);
        $this->audit->record('website.import', 'website', (string) $id, AuditLogger::SUCCESS, ['domain' => $row['domain'], 'source' => $plan['source'], 'job' => $job]);
        return [$id, $job];
    }

    /** @return array<string, mixed>|null */
    public function importOf(int $websiteId): ?array
    {
        return $this->db->fetch('SELECT * FROM website_imports WHERE website_id = ? ORDER BY id DESC LIMIT 1', [$websiteId]);
    }

    /**
     * Finish an import once its job is done: activate the website and
     * register the application's database, or clean up after a failure.
     *
     * @param array<string, mixed> $site
     * @return bool whether the website still exists
     */
    public function refresh(array $site): bool
    {
        $imp = $this->importOf((int) $site['id']);
        if ($imp === null || $imp['status'] !== 'running') {
            return true;
        }
        try {
            $job = (array) $this->agent->call('jobs.get', ['id' => $imp['job_id']]);
        } catch (AgentException) {
            return true;
        }
        $status = (string) ($job['status'] ?? '');
        if ($status === 'succeeded') {
            $this->db->run("UPDATE website_imports SET status = 'succeeded', finished_at = ? WHERE id = ?", [gmdate('c'), (int) $imp['id']]);
            $this->sites->setStatus((int) $site['id'], 'active');
            $dbName = (string) $imp['database_name'];
            if ($dbName !== '' && $this->db->scalar('SELECT COUNT(*) FROM databases WHERE server_id = ? AND name = ?', [$this->server->id(), $dbName]) === 0) {
                // Track the existing database (it is not moved or changed).
                try {
                    $names = array_column((array) (((array) $this->agent->call('db.overview'))['databases'] ?? []), 'name');
                } catch (AgentException) {
                    $names = [];
                }
                if (in_array($dbName, $names, true)) {
                    $this->db->run('INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (?, ?, ?, ?, ?)', [$this->server->id(), (int) $site['id'], $site['owner_id'], $dbName, gmdate('c')]);
                }
            }
            $this->discovery->invalidate();
            $this->audit->record('website.import_done', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain']]);
            return true;
        }
        if ($status === 'failed') {
            $this->db->run("UPDATE website_imports SET status = 'failed', error = ?, finished_at = ? WHERE id = ?", [mb_substr((string) ($job['error'] ?? ''), 0, 500), gmdate('c'), (int) $imp['id']]);
            $this->removeNewSite($site);
            $this->audit->record('website.import_done', 'website', (string) $site['id'], AuditLogger::FAILURE, ['domain' => $site['domain'], 'error' => $job['error'] ?? '']);
            return false;
        }
        return true;
    }

    /** @param array<string, mixed> $site */
    private function removeNewSite(array $site): void
    {
        try {
            $this->agent->call('sites.delete', ['domain' => $site['domain'], 'user' => $site['system_user'], 'remove_files' => true, 'remove_user' => true]);
        } catch (AgentException) {
            // Nothing was created on the server, or it is already gone.
        }
        $this->sites->delete((int) $site['id']);
        $this->discovery->invalidate();
    }

    /**
     * Undo an import: the old configuration serves the website again and
     * the WhaCP copy is removed (the original files were never touched).
     *
     * @param array<string, mixed> $site
     */
    public function undo(array $site): void
    {
        $imp = $this->importOf((int) $site['id']);
        if ($imp === null || $imp['status'] !== 'succeeded') {
            throw new UserFacingException('This website was not imported, or its import is not finished.');
        }
        $this->agent->call('adopt.undo', ['spec' => WebsiteService::spec($site, false), 'source' => $imp['source']]);
        $this->db->run('DELETE FROM databases WHERE website_id = ? AND name = ?', [(int) $site['id'], (string) $imp['database_name']]);
        $this->db->run("UPDATE website_imports SET status = 'undone', finished_at = ? WHERE id = ?", [gmdate('c'), (int) $imp['id']]);
        $this->removeNewSite($site);
        $this->audit->record('website.import_undo', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'source' => $imp['source']]);
    }
}
