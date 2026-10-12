<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;

/**
 * Node.js and Python runtimes on the server. The agent installs Node.js
 * versions side by side (verified nodejs.org tarballs) and reports what is
 * available; a website's workers can then run on a chosen runtime.
 */
final class RuntimeService
{
    public function __construct(
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
    ) {
    }

    /**
     * Installed and system runtimes.
     *
     * @return array{arch: string, node: list<array<string, mixed>>, python: list<array<string, mixed>>, system: list<array<string, mixed>>}
     */
    public function status(): array
    {
        $s = (array) $this->agent->call('runtime.status');
        return [
            'arch' => (string) ($s['arch'] ?? ''),
            'node' => array_map(static fn ($r): array => (array) $r, (array) ($s['node'] ?? [])),
            'python' => array_map(static fn ($r): array => (array) $r, (array) ($s['python'] ?? [])),
            'system' => array_map(static fn ($r): array => (array) $r, (array) ($s['system'] ?? [])),
        ];
    }

    /**
     * The runtimes a worker may select: the managed Node and Python runtimes,
     * as selection strings ("node:20.17.0").
     *
     * @return list<string>
     */
    public function selectable(): array
    {
        $out = [];
        try {
            $s = $this->status();
        } catch (\WhaCP\Agent\AgentException) {
            return [];
        }
        foreach (['node', 'python'] as $kind) {
            foreach ($s[$kind] as $rt) {
                $out[] = $kind . ':' . (string) ($rt['version'] ?? '');
            }
        }
        return $out;
    }

    /** Start installing a Node.js version; returns the agent job id. */
    public function installNode(string $version, ?int $userId): string
    {
        $res = (array) $this->agent->call('runtime.install', ['kind' => 'node', 'version' => $version], 600);
        $this->audit->record('runtime.install', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['kind' => 'node', 'version' => $version], $userId);
        return (string) ($res['job_id'] ?? '');
    }

    public function remove(string $kind, string $version, ?int $userId): void
    {
        $this->agent->call('runtime.remove', ['kind' => $kind, 'version' => $version], 120);
        $this->audit->record('runtime.remove', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['kind' => $kind, 'version' => $version], $userId);
    }
}
