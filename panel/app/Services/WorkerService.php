<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Support\ValidationException;

/**
 * Long-running processes (queue workers, Node.js/Python apps) run by
 * systemd as the website account: whacp-worker-<slug>.service.
 */
final class WorkerService
{
    public const RESTART = ['always' => 'Always', 'on-failure' => 'On failure', 'no' => 'Never'];

    /** How many identical copies of one worker may run (matches the agent). */
    public const MAX_PROCS = 20;

    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
        private readonly ?PlanLimits $limits = null,
    ) {
    }

    /**
     * @param array<string, mixed> $site
     * @return list<array<string, mixed>>
     */
    public function forSite(array $site): array
    {
        return array_map([self::class, 'hydrate'], $this->db->fetchAll('SELECT * FROM workers WHERE website_id = ? ORDER BY id', [(int) $site['id']]));
    }

    /** @return array<string, mixed>|null @param array<string, mixed> $site */
    public function find(array $site, int $id): ?array
    {
        $row = $this->db->fetch('SELECT * FROM workers WHERE id = ? AND website_id = ?', [$id, (int) $site['id']]);
        return $row === null ? null : self::hydrate($row);
    }

    /**
     * @param array<string, mixed> $r
     * @return array<string, mixed>
     */
    private static function hydrate(array $r): array
    {
        $env = json_decode((string) $r['env'], true);
        $r['env'] = is_array($env) ? $env : [];
        $r['enabled'] = (int) $r['enabled'] === 1;
        $r['procs'] = max(1, (int) ($r['procs'] ?? 1));
        $r['unit'] = 'whacp-worker-' . $r['slug'] . '.service';
        return $r;
    }

    /**
     * systemd state per unit (empty when the agent is unreachable).
     *
     * @param list<array<string, mixed>> $workers
     * @return array<string, array<string, mixed>>
     */
    public function status(array $workers): array
    {
        if ($workers === []) {
            return [];
        }
        try {
            $rows = (array) $this->agent->call('workers.status', ['slugs' => array_map(static fn (array $w): string => (string) $w['slug'], $workers)]);
        } catch (AgentException) {
            return [];
        }
        $out = [];
        foreach ($rows as $r) {
            $r = (array) $r;
            $out[(string) ($r['name'] ?? $r['unit'] ?? '')] = $r;
        }
        return $out;
    }

    /**
     * @param array<string, mixed> $input
     * @return array<string, mixed>
     */
    public static function validate(array $input): array
    {
        $errors = [];
        $name = trim((string) ($input['name'] ?? ''));
        if ($name === '' || mb_strlen($name) > 100) {
            $errors['name'] = 'Enter a name (up to 100 characters).';
        }
        $command = trim((string) ($input['command'] ?? ''));
        if ($command === '' || strlen($command) > 2000 || preg_match('/[\x00-\x08\x0a-\x1f\x7f]/', $command)) {
            $errors['command'] = 'Enter the command on a single line, e.g. php artisan queue:work --sleep=3.';
        }
        $dir = trim((string) ($input['directory'] ?? ''), " /");
        if ($dir !== '' && (!preg_match('~^[A-Za-z0-9._/-]{1,200}$~', $dir) || preg_match('~(^|/)\.\.(/|$)~', $dir))) {
            $errors['directory'] = 'Use a folder inside the website, e.g. app or public_html.';
        }
        $restart = (string) ($input['restart'] ?? 'always');
        if (!isset(self::RESTART[$restart])) {
            $errors['restart'] = 'Choose a restart policy.';
        }
        $mem = (int) ($input['memory_mb'] ?? 0);
        if ($mem < 0 || $mem > 65536) {
            $errors['memory_mb'] = 'Enter 0 (no limit) up to 65536 MB.';
        }
        $procs = (int) ($input['procs'] ?? 1);
        if ($procs < 1 || $procs > self::MAX_PROCS) {
            $errors['procs'] = 'Run 1 to ' . self::MAX_PROCS . ' copies.';
        }
        $runtime = trim((string) ($input['runtime'] ?? ''));
        if ($runtime === 'system') {
            $runtime = '';
        }
        if ($runtime !== '' && preg_match('/^(node|python):[0-9][0-9A-Za-z.]{0,31}$/', $runtime) !== 1) {
            $errors['runtime'] = 'Choose a runtime.';
        }
        $env = [];
        foreach (preg_split('/\R/', (string) ($input['env'] ?? '')) ?: [] as $line) {
            $line = trim($line);
            if ($line === '' || str_starts_with($line, '#')) {
                continue;
            }
            [$k, $v] = array_pad(explode('=', $line, 2), 2, null);
            if ($v === null || !preg_match('/^[A-Z_][A-Z0-9_]{0,63}$/', $k)) {
                $errors['env'] = 'Write one KEY=value per line; keys use capital letters, digits and _.';
                break;
            }
            $env[$k] = $v;
        }
        if (count($env) > 50) {
            $errors['env'] = 'At most 50 variables.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        return ['name' => $name, 'command' => $command, 'directory' => $dir, 'restart' => $restart, 'memory_mb' => $mem, 'procs' => $procs, 'runtime' => $runtime, 'env' => $env, 'enabled' => filter_var($input['enabled'] ?? true, FILTER_VALIDATE_BOOL)];
    }

    /**
     * Create or update a worker and apply it on the server.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $input
     */
    public function save(array $site, ?int $id, array $input): void
    {
        $v = self::validate($input);
        $now = gmdate('c');
        $previous = $id === null ? null : $this->find($site, $id);
        if ($id === null) {
            $this->limits?->assertCanCreate('workers', $site['owner_id'] === null ? null : (int) $site['owner_id']);
            $slug = $this->slug($site, $v['name']);
            $this->db->run(
                'INSERT INTO workers (server_id, website_id, slug, name, directory, command, env, restart, memory_mb, procs, runtime, enabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
                [$this->server->id(), (int) $site['id'], $slug, $v['name'], $v['directory'], $v['command'], json_encode($v['env'], JSON_THROW_ON_ERROR | JSON_FORCE_OBJECT), $v['restart'], $v['memory_mb'], $v['procs'], $v['runtime'], $v['enabled'] ? 1 : 0, $now, $now]
            );
            $id = $this->db->lastInsertId();
        } else {
            $this->db->run(
                'UPDATE workers SET name = ?, directory = ?, command = ?, env = ?, restart = ?, memory_mb = ?, procs = ?, runtime = ?, enabled = ?, updated_at = ? WHERE id = ? AND website_id = ?',
                [$v['name'], $v['directory'], $v['command'], json_encode($v['env'], JSON_THROW_ON_ERROR | JSON_FORCE_OBJECT), $v['restart'], $v['memory_mb'], $v['procs'], $v['runtime'], $v['enabled'] ? 1 : 0, $now, $id, (int) $site['id']]
            );
        }
        $worker = $this->find($site, $id);
        try {
            $this->apply($site, (array) $worker);
        } catch (\Throwable $e) {
            if ($previous === null) {
                $this->db->run('DELETE FROM workers WHERE id = ?', [$id]);
            } else {
                $this->db->run(
                    'UPDATE workers SET name = ?, directory = ?, command = ?, env = ?, restart = ?, memory_mb = ?, procs = ?, runtime = ?, enabled = ?, updated_at = ? WHERE id = ?',
                    [$previous['name'], $previous['directory'], $previous['command'], json_encode($previous['env'], JSON_FORCE_OBJECT), $previous['restart'], $previous['memory_mb'], max(1, (int) ($previous['procs'] ?? 1)), (string) ($previous['runtime'] ?? ''), $previous['enabled'] ? 1 : 0, $previous['updated_at'], $id]
                );
            }
            $this->audit->record($previous === null ? 'worker.create' : 'worker.update', 'website', (string) $site['id'], AuditLogger::FAILURE, ['domain' => $site['domain'], 'worker' => $v['name'], 'error' => $e->getMessage()]);
            throw $e;
        }
        $this->audit->record($previous === null ? 'worker.create' : 'worker.update', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'worker' => $v['name'], 'command' => $v['command']]);
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $worker
     */
    public function apply(array $site, array $worker): void
    {
        $this->agent->call('workers.apply', [
            'slug' => $worker['slug'],
            'name' => $worker['name'],
            'domain' => $site['domain'],
            'user' => $site['system_user'],
            'root' => $site['root'],
            'directory' => $worker['directory'],
            'command' => $worker['command'],
            'env' => (object) $worker['env'],
            'restart' => $worker['restart'],
            'memory_mb' => (int) $worker['memory_mb'],
            'procs' => max(1, (int) ($worker['procs'] ?? 1)),
            'runtime' => (string) ($worker['runtime'] ?? ''),
            'enabled' => (bool) $worker['enabled'] && (bool) $site['enabled'] && empty($site['account_suspended']),
        ], 120);
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $worker
     */
    public function setEnabled(array $site, array $worker, bool $enabled): void
    {
        $this->apply($site, ['enabled' => $enabled] + $worker);
        $this->db->run('UPDATE workers SET enabled = ?, updated_at = ? WHERE id = ?', [$enabled ? 1 : 0, gmdate('c'), (int) $worker['id']]);
        $this->audit->record($enabled ? 'worker.start' : 'worker.stop', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'worker' => $worker['name']]);
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $worker
     */
    public function restart(array $site, array $worker): void
    {
        if (!empty($site['account_suspended'])) {
            throw new \WhaCP\Support\UserFacingException('The account is suspended; its workers are stopped.');
        }
        $this->agent->call('workers.restart', ['slug' => $worker['slug']], 120);
        $this->audit->record('worker.restart', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'worker' => $worker['name']]);
    }

    /**
     * @param array<string, mixed> $site
     * @param array<string, mixed> $worker
     */
    public function delete(array $site, array $worker): void
    {
        $this->agent->call('workers.remove', ['slug' => $worker['slug']], 120);
        $this->db->run('DELETE FROM workers WHERE id = ?', [(int) $worker['id']]);
        $this->audit->record('worker.delete', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'worker' => $worker['name']]);
    }

    /** Remove every worker of a website from the server (before the website is deleted). @param array<string, mixed> $site */
    public function removeAll(array $site): void
    {
        foreach ($this->forSite($site) as $w) {
            try {
                $this->agent->call('workers.remove', ['slug' => $w['slug']], 120);
            } catch (AgentException) {
                // The website deletion continues; the unit is reported by discovery.
            }
        }
    }

    /** @param array<string, mixed> $site */
    private function slug(array $site, string $name): string
    {
        $base = strtolower(preg_replace('/[^a-z0-9]+/i', '-', explode('.', (string) $site['domain'])[0] . '-' . $name) ?? '');
        $base = substr(trim($base, '-'), 0, 50) ?: 'worker';
        $slug = $base;
        for ($i = 2; $this->db->scalar('SELECT COUNT(*) FROM workers WHERE server_id = ? AND slug = ?', [$this->server->id(), $slug]) > 0; $i++) {
            $slug = $base . '-' . $i;
        }
        return $slug;
    }
}
