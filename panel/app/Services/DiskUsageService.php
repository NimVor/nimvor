<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;
use NimVor\Support\UserFacingException;

/**
 * Disk usage by folder of a website: the agent measures in a background
 * job; the latest result per folder is kept here.
 */
final class DiskUsageService
{
    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
    ) {
    }

    /** A folder inside the website folder ("" for the website folder itself). */
    public static function cleanPath(string $path): string
    {
        $path = trim(str_replace('\\', '/', $path), " /");
        if ($path === '') {
            return '';
        }
        $parts = explode('/', $path);
        foreach ($parts as $p) {
            if ($p === '' || $p === '.' || $p === '..' || str_contains($p, "\0")) {
                throw new UserFacingException('That is not a folder inside the website folder.');
            }
        }
        if (strlen($path) > 1024) {
            throw new UserFacingException('The folder path is too long.');
        }
        return $path;
    }

    /**
     * Start measuring a folder; returns the agent job id.
     *
     * @param array<string, mixed> $site
     */
    public function scan(array $site, string $path): string
    {
        $path = self::cleanPath($path);
        if ($this->running((int) $site['id'], $path) !== null) {
            throw new UserFacingException('This folder is being measured already.');
        }
        $res = (array) $this->agent->call('sites.disk_usage', ['domain' => (string) $site['domain'], 'path' => $path]);
        $job = (string) ($res['job_id'] ?? '');
        $this->db->run(
            "INSERT INTO disk_usage_scans (server_id, website_id, path, job_id, status, started_at) VALUES (?, ?, ?, ?, 'running', ?)",
            [$this->server->id(), (int) $site['id'], $path, $job, gmdate('c')]
        );
        return $job;
    }

    /** @return array<string, mixed>|null */
    public function running(int $websiteId, string $path): ?array
    {
        return $this->db->fetch("SELECT * FROM disk_usage_scans WHERE website_id = ? AND path = ? AND status = 'running' ORDER BY id DESC LIMIT 1", [$websiteId, $path]);
    }

    /**
     * The latest finished scan of a folder, with its entries.
     *
     * @return array<string, mixed>|null
     */
    public function latest(int $websiteId, string $path): ?array
    {
        $row = $this->db->fetch("SELECT * FROM disk_usage_scans WHERE website_id = ? AND path = ? AND status <> 'running' ORDER BY id DESC LIMIT 1", [$websiteId, $path]);
        if ($row === null) {
            return null;
        }
        $r = json_decode((string) $row['result'], true);
        $row['result'] = is_array($r) ? $r : [];
        return $row;
    }

    /** Record the results of this website's finished scans. */
    public function refresh(int $websiteId): void
    {
        foreach ($this->db->fetchAll("SELECT * FROM disk_usage_scans WHERE website_id = ? AND server_id = ? AND status = 'running'", [$websiteId, $this->server->id()]) as $scan) {
            try {
                $job = (array) $this->agent->call('jobs.get', ['id' => $scan['job_id']]);
            } catch (AgentException $e) {
                if ($e->errorCode() === 'not_found') {
                    $this->finish((int) $scan['id'], 'failed', [], 'The scan was interrupted (the agent restarted).');
                }
                continue;
            }
            $status = (string) ($job['status'] ?? '');
            if ($status === 'failed') {
                $this->finish((int) $scan['id'], 'failed', [], (string) ($job['error'] ?? 'The scan failed.'));
            } elseif ($status === 'succeeded') {
                $this->finish((int) $scan['id'], 'done', (array) json_decode((string) json_encode($job['result'] ?? []), true), '');
            }
        }
        // Keep the five latest scans of each folder.
        $this->db->run(
            'DELETE FROM disk_usage_scans WHERE website_id = ? AND status <> \'running\' AND id NOT IN (SELECT id FROM disk_usage_scans d WHERE d.website_id = disk_usage_scans.website_id AND d.path = disk_usage_scans.path ORDER BY id DESC LIMIT 5)',
            [$websiteId]
        );
    }

    /** @param array<string, mixed> $result */
    private function finish(int $id, string $status, array $result, string $error): void
    {
        $entries = [];
        foreach ((array) ($result['entries'] ?? []) as $e) {
            $e = (array) $e;
            $entries[] = ['name' => mb_substr((string) ($e['name'] ?? ''), 0, 255), 'kind' => in_array($e['kind'] ?? '', ['dir', 'files', 'mail'], true) ? $e['kind'] : 'dir', 'bytes' => max(0, (int) ($e['bytes'] ?? 0)), 'files' => max(0, (int) ($e['files'] ?? 0))];
        }
        $clean = ['entries' => array_slice($entries, 0, 300), 'bytes' => max(0, (int) ($result['bytes'] ?? 0)), 'partial' => !empty($result['partial']), 'truncated' => max(0, (int) ($result['truncated'] ?? 0))];
        $this->db->run(
            "UPDATE disk_usage_scans SET status = ?, result = ?, error = ?, finished_at = ? WHERE id = ? AND status = 'running'",
            [$status, (string) json_encode($clean), mb_substr($error, 0, 1000), gmdate('c'), $id]
        );
    }
}
