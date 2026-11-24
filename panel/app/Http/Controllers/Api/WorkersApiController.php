<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\ServerContext;
use NimVor\Services\WorkerService;
use NimVor\Support\ValidationException;

/**
 * REST API v1: a website's background workers (queue workers, Node.js and Python apps run by systemd as
 * the website's account). Every endpoint calls WorkerService, as the web interface does: the name,
 * command, folder and variables are validated, plan limits apply, the unit is written and started by the
 * agent and a refused change is undone. The values of a worker's environment variables are never
 * returned (only their names), because they often hold secrets.
 */
final class WorkersApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly WorkerService $workers,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
    ) {
    }

    /** @return array<string, mixed>|null */
    private function site(Request $r, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($r));
    }

    /**
     * @param array<string, mixed> $w
     * @param array<string, mixed>|null $state
     * @return array<string, mixed>
     */
    private static function workerOut(array $w, ?array $state): array
    {
        return [
            'id' => (int) $w['id'], 'name' => (string) $w['name'], 'unit' => (string) $w['unit'], 'command' => (string) $w['command'],
            'directory' => (string) $w['directory'], 'runtime' => (string) ($w['runtime'] ?? ''), 'restart' => (string) $w['restart'],
            'procs' => (int) $w['procs'], 'memory_mb' => (int) $w['memory_mb'], 'enabled' => (bool) $w['enabled'],
            'env_keys' => array_values(array_map('strval', array_keys((array) $w['env']))),
            'state' => $state === null ? null : [
                'active' => (string) ($state['active_state'] ?? ''), 'sub' => (string) ($state['sub_state'] ?? ''),
                'running' => (bool) ($state['running'] ?? false), 'procs_running' => (int) ($state['procs_running'] ?? 0),
                'memory_bytes' => isset($state['memory_bytes']) ? (int) $state['memory_bytes'] : null, 'active_since' => $state['active_since'] ?? null,
            ],
            'created_at' => $w['created_at'], 'updated_at' => $w['updated_at'],
        ];
    }

    /**
     * @param array<string, mixed> $s
     * @param array<string, mixed> $w
     * @return array<string, mixed>
     */
    private function fresh(array $s, array $w): array
    {
        $w = (array) $this->workers->find($s, (int) $w['id']);
        return self::workerOut($w, $this->workers->status([$w])[$w['unit']] ?? null);
    }

    /**
     * What the worker form takes, from the JSON body: env as an object of names and values.
     *
     * @param array<string, mixed> $in
     * @return array<string, mixed>
     */
    private static function formFrom(array $in): array
    {
        if (array_key_exists('env', $in)) {
            if (!is_array($in['env'])) {
                throw new ValidationException(['env' => 'Send an object of variables, for example {"QUEUE": "default"}.']);
            }
            $lines = [];
            foreach ($in['env'] as $k => $v) {
                if (!is_scalar($v) || preg_match('/[\r\n]/', (string) $k . (string) $v)) {
                    throw new ValidationException(['env' => 'Variable ' . $k . ' must be a single-line text.']);
                }
                $lines[] = $k . '=' . $v;
            }
            $in['env'] = implode("\n", $lines);
        }
        return $in;
    }

    public function listWorkers(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s): Response {
            $list = $this->workers->forSite($s);
            $status = $this->workers->status($list);
            return self::ok(array_map(fn (array $w): array => self::workerOut($w, $status[$w['unit']] ?? null), $list));
        });
    }

    public function showWorker(Request $r, string $id, string $worker): Response
    {
        $s = $this->site($r, $id);
        $w = $s === null ? null : $this->workers->find($s, (int) $worker);
        return $w === null ? self::notFound() : self::ok($this->fresh((array) $s, $w));
    }

    public function createWorker(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $before = array_column($this->workers->forSite($s), 'id');
            $this->workers->save($s, null, self::formFrom(self::body($r)));
            $new = array_values(array_filter($this->workers->forSite($s), static fn (array $w): bool => !in_array($w['id'], $before, true)));
            return self::ok($this->fresh($s, (array) $new[0]), 201);
        });
    }

    public function updateWorker(Request $r, string $id, string $worker): Response
    {
        $s = $this->site($r, $id);
        $w = $s === null ? null : $this->workers->find($s, (int) $worker);
        if ($s === null || $w === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s, $w): Response {
            $env = [];
            foreach ((array) $w['env'] as $k => $v) {
                $env[] = $k . '=' . $v;
            }
            // A PATCH changes only what it names; the rest stays as it is.
            $in = self::formFrom(self::body($r)) + [
                'name' => $w['name'], 'command' => $w['command'], 'directory' => $w['directory'], 'runtime' => $w['runtime'] ?? '',
                'env' => implode("\n", $env), 'restart' => $w['restart'], 'procs' => $w['procs'], 'memory_mb' => $w['memory_mb'], 'enabled' => $w['enabled'],
            ];
            $this->workers->save($s, (int) $w['id'], $in);
            return self::ok($this->fresh($s, $w));
        });
    }

    public function deleteWorker(Request $r, string $id, string $worker): Response
    {
        $s = $this->site($r, $id);
        $w = $s === null ? null : $this->workers->find($s, (int) $worker);
        if ($s === null || $w === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s, $w): Response {
            $this->workers->delete($s, $w);
            return self::ok(['deleted' => (int) $w['id']]);
        });
    }

    public function restartWorker(Request $r, string $id, string $worker): Response
    {
        $s = $this->site($r, $id);
        $w = $s === null ? null : $this->workers->find($s, (int) $worker);
        if ($s === null || $w === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($s, $w): Response {
            $this->workers->restart($s, $w);
            return self::ok($this->fresh($s, $w));
        });
    }
}
