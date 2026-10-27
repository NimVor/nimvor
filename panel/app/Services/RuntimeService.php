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
     * The runtimes a worker (or a Node.js/Python site) may select, as
     * selection strings ("node:20.17.0", "python:3.12"). This includes the
     * WhaCP-managed runtimes and the system's own Python interpreters (which
     * come from the distribution or Packages), since there is no managed
     * Python install path. The system Node is offered as "system" instead.
     *
     * @return list<string>
     */
    public function selectable(): array
    {
        try {
            $s = $this->status();
        } catch (\WhaCP\Agent\AgentException) {
            return [];
        }
        $out = [];
        $seen = [];
        $add = static function (string $sel) use (&$out, &$seen): void {
            if (!isset($seen[$sel])) {
                $seen[$sel] = true;
                $out[] = $sel;
            }
        };
        foreach (['node', 'python'] as $kind) {
            foreach ($s[$kind] as $rt) {
                $add($kind . ':' . (string) ($rt['version'] ?? ''));
            }
        }
        // System Python interpreters (python3.10, python3.11, …) are
        // selectable by their version too; the system Node is "system".
        foreach ($s['system'] as $rt) {
            $v = (string) ($rt['version'] ?? '');
            if (($rt['kind'] ?? '') === 'python' && $v !== '' && $v !== 'system') {
                $add('python:' . $v);
            }
        }
        return $out;
    }

    /**
     * The stable Node.js and Python versions that can be installed, with
     * whether each is already installed. A source that could not be reached
     * is reported in node_error / python_error.
     *
     * @return array{node: list<array<string, mixed>>, python: list<array<string, mixed>>, node_error: string, python_error: string}
     */
    public function available(): array
    {
        $a = (array) $this->agent->call('runtime.available', [], 60);
        return [
            'node' => array_map(static fn ($r): array => (array) $r, (array) ($a['node'] ?? [])),
            'python' => array_map(static fn ($r): array => (array) $r, (array) ($a['python'] ?? [])),
            'node_error' => (string) ($a['node_error'] ?? ''),
            'python_error' => (string) ($a['python_error'] ?? ''),
        ];
    }

    /** Start installing a Node.js version; returns the agent job id. */
    public function installNode(string $version, ?int $userId): string
    {
        return $this->install('node', $version, $userId);
    }

    /** Start installing a runtime version (node or python); returns the agent job id. */
    public function install(string $kind, string $version, ?int $userId): string
    {
        $res = (array) $this->agent->call('runtime.install', ['kind' => $kind, 'version' => $version], 600);
        $this->audit->record('runtime.install', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['kind' => $kind, 'version' => $version], $userId);
        return (string) ($res['job_id'] ?? '');
    }

    public function remove(string $kind, string $version, ?int $userId): void
    {
        $this->agent->call('runtime.remove', ['kind' => $kind, 'version' => $version], 120);
        $this->audit->record('runtime.remove', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['kind' => $kind, 'version' => $version], $userId);
    }
}
