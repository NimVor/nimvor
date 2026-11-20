<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Database\Connection;
use NimVor\Support\UserFacingException;

/**
 * Server → Disk usage: the filesystems, a scan of where the space goes (a
 * background job in the agent, its latest results kept here) and removal of
 * large files picked from the latest scan.
 */
final class ServerDiskService
{
    /** Files one request may remove. */
    public const MAX_REMOVE = 200;

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
    ) {
    }

    /** @return list<array<string, mixed>> */
    public function filesystems(): array
    {
        return array_values(array_map(static fn ($f): array => (array) $f, (array) $this->agent->call('disk.filesystems')));
    }

    /** Starts a scan; returns the agent job id. */
    public function scan(int $minMb): string
    {
        $minMb = max(1, min(100000, $minMb));
        if ($this->running() !== null) {
            throw new UserFacingException('The disk is being measured already.');
        }
        $res = (array) $this->agent->call('disk.scan', ['min_mb' => $minMb]);
        $job = (string) ($res['job_id'] ?? '');
        $this->db->run("INSERT INTO server_disk_scans (server_id, job_id, status, min_mb, started_at) VALUES (?, ?, 'running', ?, ?)", [$this->server->id(), $job, $minMb, gmdate('c')]);
        return $job;
    }

    /** @return array<string, mixed>|null */
    public function running(): ?array
    {
        return $this->db->fetch("SELECT * FROM server_disk_scans WHERE server_id = ? AND status = 'running' ORDER BY id DESC LIMIT 1", [$this->server->id()]);
    }

    /** @return array<string, mixed>|null the latest finished scan, its result decoded */
    public function latest(): ?array
    {
        $row = $this->db->fetch("SELECT * FROM server_disk_scans WHERE server_id = ? AND status <> 'running' ORDER BY id DESC LIMIT 1", [$this->server->id()]);
        if ($row === null) {
            return null;
        }
        $r = json_decode((string) $row['result'], true);
        $row['result'] = is_array($r) ? $r : [];
        return $row;
    }

    /** Records the result of a finished scan. */
    public function refresh(): void
    {
        foreach ($this->db->fetchAll("SELECT * FROM server_disk_scans WHERE server_id = ? AND status = 'running'", [$this->server->id()]) as $scan) {
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
        $this->db->run('DELETE FROM server_disk_scans WHERE server_id = ? AND status <> \'running\' AND id NOT IN (SELECT id FROM server_disk_scans WHERE server_id = ? ORDER BY id DESC LIMIT 5)', [$this->server->id(), $this->server->id()]);
    }

    /** @param array<string, mixed> $result */
    private function finish(int $id, string $status, array $result, string $error): void
    {
        $this->db->run(
            "UPDATE server_disk_scans SET status = ?, result = ?, error = ?, finished_at = ? WHERE id = ? AND status = 'running'",
            [$status, (string) json_encode($result), mb_substr($error, 0, 1000), gmdate('c'), $id]
        );
    }

    /**
     * Removes files picked from the latest scan (a path the scan did not list
     * is refused here, and the agent checks every path again).
     *
     * @param list<string> $paths
     * @return array{results: list<array<string, mixed>>, freed_bytes: int}
     */
    public function remove(array $paths): array
    {
        $paths = array_values(array_unique(array_filter(array_map('strval', $paths), static fn (string $p): bool => $p !== '')));
        if ($paths === []) {
            throw new UserFacingException('Choose at least one file.');
        }
        if (count($paths) > self::MAX_REMOVE) {
            throw new UserFacingException('Choose at most ' . self::MAX_REMOVE . ' files at a time.');
        }
        $scan = $this->latest();
        $listed = [];
        foreach ((array) ($scan['result']['large'] ?? []) as $f) {
            if (!empty(((array) $f)['deletable'])) {
                $listed[(string) ((array) $f)['path']] = true;
            }
        }
        $unknown = array_values(array_filter($paths, static fn (string $p): bool => !isset($listed[$p])));
        if ($unknown !== []) {
            throw new UserFacingException('Those files are not in the latest scan (scan again): ' . implode(', ', array_slice($unknown, 0, 3)) . '.');
        }
        $res = (array) $this->agent->call('disk.remove', ['paths' => $paths], 300);
        return ['results' => array_values(array_map(static fn ($r): array => (array) $r, (array) ($res['results'] ?? []))), 'freed_bytes' => (int) ($res['freed_bytes'] ?? 0)];
    }
}
