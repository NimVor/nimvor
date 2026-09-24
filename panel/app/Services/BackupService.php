<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Hosting\Scope;
use WhaCP\Security\Crypto;
use WhaCP\Support\Events;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * Backups of websites (files + their databases) and of the server
 * configuration, stored on the server and/or in S3-compatible storage.
 *
 * The agent creates, verifies, encrypts and restores; the panel keeps the
 * list (with each backup's manifest), remote storage credentials
 * (encrypted), schedules and retention.
 */
final class BackupService
{
    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly ServerContext $server,
        private readonly Crypto $crypto,
        private readonly Events $events,
    ) {
    }

    // ---- Remote storage -------------------------------------------------

    /** @return list<array<string, mixed>> without secrets */
    public function storages(): array
    {
        return array_map(function (array $r): array {
            $cfg = $this->storageConfig((int) $r['id']);
            return ['id' => (int) $r['id'], 'name' => $r['name'], 'endpoint' => $cfg['endpoint'] ?? '', 'bucket' => $cfg['bucket'] ?? '', 'prefix' => $cfg['prefix'] ?? '', 'created_at' => $r['created_at']];
        }, $this->db->fetchAll('SELECT * FROM backup_storages ORDER BY name'));
    }

    /** @return array<string, mixed> agent "remote" parameter */
    public function storageConfig(int $id): array
    {
        $row = $this->db->fetch('SELECT * FROM backup_storages WHERE id = ?', [$id]);
        if ($row === null) {
            throw new UserFacingException('The remote storage no longer exists.');
        }
        return (array) json_decode($this->crypto->decrypt((string) $row['config_enc']), true);
    }

    /** @param array<string, mixed> $input */
    public function saveStorage(array $input, bool $test = true): int
    {
        $errors = [];
        $name = trim((string) ($input['name'] ?? ''));
        if ($name === '' || mb_strlen($name) > 60) {
            $errors['name'] = 'Enter a name (up to 60 characters).';
        }
        $endpoint = rtrim(trim((string) ($input['endpoint'] ?? '')), '/');
        if (!preg_match('#^https://[A-Za-z0-9.-]+(:[0-9]+)?$#', $endpoint) && !preg_match('#^http://(127\.0\.0\.1|localhost)(:[0-9]+)?$#', $endpoint)) {
            $errors['endpoint'] = 'Enter the https:// endpoint, e.g. https://s3.eu-central-1.amazonaws.com.';
        }
        $bucket = trim((string) ($input['bucket'] ?? ''));
        if (!preg_match('/^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/', $bucket)) {
            $errors['bucket'] = 'Enter a valid bucket name.';
        }
        $region = trim((string) ($input['region'] ?? '')) ?: 'us-east-1';
        if (!preg_match('/^[a-z0-9-]{1,40}$/', $region)) {
            $errors['region'] = 'Enter a region such as eu-central-1 (or us-east-1 if unsure).';
        }
        $prefix = trim((string) ($input['prefix'] ?? ''), " /");
        if (!preg_match('#^[A-Za-z0-9._/-]{0,200}$#', $prefix) || str_contains($prefix, '..')) {
            $errors['prefix'] = 'Use letters, digits and . _ / -';
        }
        $access = trim((string) ($input['access_key'] ?? ''));
        $secret = trim((string) ($input['secret_key'] ?? ''));
        if ($access === '' || $secret === '') {
            $errors['access_key'] = 'Enter the access key and secret key.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $cfg = ['endpoint' => $endpoint, 'region' => $region, 'bucket' => $bucket, 'prefix' => $prefix, 'access_key' => $access, 'secret_key' => $secret, 'path_style' => filter_var($input['path_style'] ?? false, FILTER_VALIDATE_BOOL)];
        if ($test) {
            $this->agent->call('backup.remote_test', ['remote' => $cfg], 60);
        }
        if ($this->db->scalar('SELECT COUNT(*) FROM backup_storages WHERE name = ?', [$name]) > 0) {
            throw new ValidationException(['name' => 'A storage with this name exists.']);
        }
        $this->db->run('INSERT INTO backup_storages (name, kind, config_enc, created_at) VALUES (?, ?, ?, ?)', [$name, 's3', $this->crypto->encrypt((string) json_encode($cfg)), gmdate('c')]);
        $id = $this->db->lastInsertId();
        $this->audit->record('backup.storage_create', 'backup_storage', (string) $id, AuditLogger::SUCCESS, ['name' => $name, 'endpoint' => $endpoint, 'bucket' => $bucket]);
        return $id;
    }

    public function deleteStorage(int $id): void
    {
        if ($this->db->scalar("SELECT COUNT(*) FROM backups WHERE storage_id = ? AND local = 0 AND status = 'completed'", [$id]) > 0) {
            throw new UserFacingException('Some backups are only stored there.', 'Deleting the storage would make them unreachable from WhaCP.', 'Delete those backups first.');
        }
        $this->db->run('DELETE FROM backup_storages WHERE id = ?', [$id]);
        $this->audit->record('backup.storage_delete', 'backup_storage', (string) $id);
    }

    // ---- Backups --------------------------------------------------------

    /**
     * @param array<string, mixed> $site
     * @return list<string>
     */
    public function siteDatabases(array $site): array
    {
        return array_map(static fn (array $r): string => (string) $r['name'], $this->db->fetchAll('SELECT name FROM databases WHERE website_id = ? ORDER BY name', [(int) $site['id']]));
    }

    /**
     * Start a website backup; returns the agent job id.
     *
     * @param array<string, mixed> $site
     * @param array{files?: bool, databases?: bool, encrypt?: bool, storage_id?: int|null, keep_local?: bool, label?: string, schedule_id?: int|null} $opts
     */
    public function createSite(array $site, array $opts, ?int $userId): string
    {
        $files = (bool) ($opts['files'] ?? true);
        $dbs = ($opts['databases'] ?? true) ? $this->siteDatabases($site) : [];
        if (!$files && $dbs === []) {
            throw new UserFacingException('Nothing to back up.', 'Choose the website files, or link a database to this website.');
        }
        $label = mb_substr(trim((string) ($opts['label'] ?? '')), 0, 100);
        $storageId = $opts['storage_id'] ?? null;
        $params = [
            'domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'],
            'files' => $files, 'databases' => $dbs, 'encrypt' => (bool) ($opts['encrypt'] ?? true), 'label' => $label,
            'meta' => ['website' => WebsiteService::spec($site, false), 'panel_version' => 1],
        ];
        if ($storageId !== null) {
            $params['remote'] = $this->storageConfig($storageId);
            $params['keep_local'] = (bool) ($opts['keep_local'] ?? true);
        }
        $res = (array) $this->agent->call('backup.create', $params);
        $job = (string) $res['job_id'];
        $this->db->run(
            'INSERT INTO backups (server_id, website_id, schedule_id, kind, domain, label, encrypted, storage_id, status, job_id, user_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
            [$this->server->id(), (int) $site['id'], $opts['schedule_id'] ?? null, 'site', $site['domain'], $label, $params['encrypt'] ? 1 : 0, $storageId, 'running', $job, $userId, gmdate('c')]
        );
        $this->audit->record('backup.create', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'files' => $files, 'databases' => $dbs, 'remote' => $storageId !== null]);
        return $job;
    }

    /** @param array{encrypt?: bool, storage_id?: int|null, keep_local?: bool, label?: string, schedule_id?: int|null} $opts */
    public function createServer(array $opts, ?int $userId): string
    {
        $storageId = $opts['storage_id'] ?? null;
        $encrypt = $storageId !== null || (bool) ($opts['encrypt'] ?? true);
        $label = mb_substr(trim((string) ($opts['label'] ?? '')), 0, 100);
        $params = ['encrypt' => $encrypt, 'label' => $label];
        if ($storageId !== null) {
            $params['remote'] = $this->storageConfig($storageId);
            $params['keep_local'] = (bool) ($opts['keep_local'] ?? true);
        }
        $res = (array) $this->agent->call('backup.server', $params);
        $job = (string) $res['job_id'];
        $this->db->run(
            'INSERT INTO backups (server_id, schedule_id, kind, label, encrypted, storage_id, status, job_id, user_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)',
            [$this->server->id(), $opts['schedule_id'] ?? null, 'server', $label, $encrypt ? 1 : 0, $storageId, 'running', $job, $userId, gmdate('c')]
        );
        $this->audit->record('backup.server', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['remote' => $storageId !== null]);
        return $job;
    }

    /** Update running backups from their agent jobs. */
    public function refresh(): void
    {
        foreach ($this->db->fetchAll("SELECT * FROM backups WHERE server_id = ? AND status = 'running'", [$this->server->id()]) as $b) {
            try {
                $job = (array) $this->agent->call('jobs.get', ['id' => $b['job_id']]);
            } catch (AgentException $e) {
                if ($e->errorCode() === 'not_found') {
                    $this->db->run("UPDATE backups SET status = 'failed', error = ?, finished_at = ? WHERE id = ?", ['The job was lost (agent restarted).', gmdate('c'), (int) $b['id']]);
                }
                continue;
            }
            $status = (string) ($job['status'] ?? '');
            if ($status === 'failed') {
                $this->db->run("UPDATE backups SET status = 'failed', error = ?, finished_at = ? WHERE id = ?", [mb_substr((string) ($job['error'] ?? ''), 0, 500), gmdate('c'), (int) $b['id']]);
                $this->events->dispatch('backup.failed', ['backup' => $b]);
            } elseif ($status === 'succeeded') {
                $m = (array) ($job['result'] ?? []);
                unset($m['meta']);
                $this->db->run(
                    "UPDATE backups SET status = 'completed', backup_id = ?, manifest = ?, size = ?, encrypted = ?, local = ?, finished_at = ? WHERE id = ?",
                    [(string) ($m['id'] ?? ''), json_encode($m, JSON_UNESCAPED_SLASHES), (int) ($m['size'] ?? 0), !empty($m['encrypted']) ? 1 : 0, !empty($m['local']) ? 1 : 0, gmdate('c'), (int) $b['id']]
                );
                $this->events->dispatch('backup.completed', ['backup' => $b]);
            }
        }
    }

    /**
     * @param array<string, mixed>|null $site null = server backups
     * @return list<array<string, mixed>>
     */
    public function list(?array $site, int $limit = 100): array
    {
        $rows = $site === null
            ? $this->db->fetchAll("SELECT b.*, s.name AS storage_name FROM backups b LEFT JOIN backup_storages s ON s.id = b.storage_id WHERE b.server_id = ? AND b.kind = 'server' ORDER BY b.id DESC LIMIT ?", [$this->server->id(), $limit])
            : $this->db->fetchAll('SELECT b.*, s.name AS storage_name FROM backups b LEFT JOIN backup_storages s ON s.id = b.storage_id WHERE b.website_id = ? ORDER BY b.id DESC LIMIT ?', [(int) $site['id'], $limit]);
        return array_map([self::class, 'hydrate'], $rows);
    }

    /**
     * Website backups visible to a user (all websites for admins).
     *
     * @param array<string, mixed> $user
     * @return list<array<string, mixed>>
     */
    public function listForUser(array $user, int $limit = 200): array
    {
        [$cond, $params] = Scope::condition($user, 'w.owner_id');
        $rows = $this->db->fetchAll(
            "SELECT b.*, s.name AS storage_name FROM backups b JOIN websites w ON w.id = b.website_id LEFT JOIN backup_storages s ON s.id = b.storage_id
             WHERE b.server_id = :server AND b.kind = 'site' AND {$cond} ORDER BY b.id DESC LIMIT :lim",
            ['server' => $this->server->id(), 'lim' => $limit] + $params
        );
        return array_map([self::class, 'hydrate'], $rows);
    }

    /**
     * @param array<string, mixed> $r
     * @return array<string, mixed>
     */
    private static function hydrate(array $r): array
    {
        $m = json_decode((string) $r['manifest'], true);
        $r['manifest'] = is_array($m) ? $m : [];
        $r['parts'] = array_map(static fn ($f): array => (array) $f, (array) ($r['manifest']['files'] ?? []));
        $r['local'] = (int) $r['local'] === 1;
        $r['encrypted'] = (int) $r['encrypted'] === 1;
        return $r;
    }

    /**
     * A backup of a website (scoped) or, with $site null, a server backup.
     *
     * @param array<string, mixed>|null $site
     * @return array<string, mixed>|null
     */
    public function find(?array $site, int $id): ?array
    {
        $row = $site === null
            ? $this->db->fetch("SELECT * FROM backups WHERE id = ? AND server_id = ? AND kind = 'server'", [$id, $this->server->id()])
            : $this->db->fetch('SELECT * FROM backups WHERE id = ? AND website_id = ?', [$id, (int) $site['id']]);
        return $row === null ? null : self::hydrate($row);
    }

    /** @param array<string, mixed> $b */
    private function target(array $b): array
    {
        $t = $b['kind'] === 'server' ? ['kind' => 'server'] : ['domain' => $b['domain']];
        return $t + ['id' => $b['backup_id']];
    }

    /** @param array<string, mixed> $b */
    public function delete(array $b): void
    {
        if ($b['status'] === 'running') {
            throw new UserFacingException('This backup is still running.');
        }
        if ($b['status'] === 'completed') {
            $params = $this->target($b);
            if ($b['storage_id'] !== null) {
                try {
                    $params['remote'] = $this->storageConfig((int) $b['storage_id']);
                } catch (UserFacingException) {
                    // storage removed: only the local copy can be deleted
                }
            }
            try {
                $this->agent->call('backup.delete', $params, 300);
            } catch (AgentException $e) {
                if ($e->errorCode() !== 'not_found') {
                    throw $e;
                }
            }
        }
        $this->db->run('DELETE FROM backups WHERE id = ?', [(int) $b['id']]);
        $this->audit->record('backup.delete', $b['kind'] === 'server' ? 'server' : 'website', (string) ($b['website_id'] ?? $b['server_id']), AuditLogger::SUCCESS, ['backup' => $b['backup_id'], 'domain' => $b['domain']]);
    }

    /**
     * Restore selected parts of a website backup; returns the job id.
     *
     * @param array<string, mixed> $site
     * @param array<string, mixed> $b
     * @param list<string> $databases source database names to restore (into the same name)
     */
    public function restore(array $site, array $b, bool $files, array $databases, string $confirm, ?int $userId): string
    {
        if ($b['status'] !== 'completed' || $b['kind'] !== 'site') {
            throw new UserFacingException('Only completed website backups can be restored.');
        }
        if ($confirm !== $site['domain']) {
            throw new UserFacingException('Type the domain name to confirm the restore.');
        }
        $available = [];
        foreach ($b['parts'] as $p) {
            if (($p['kind'] ?? '') === 'database') {
                $available[] = (string) $p['database'];
            }
        }
        $map = [];
        foreach ($databases as $d) {
            if (!in_array($d, $available, true)) {
                throw new UserFacingException('The backup does not contain database ' . $d . '.');
            }
            $map[$d] = $d;
        }
        if (!$files && $map === []) {
            throw new UserFacingException('Choose what to restore.');
        }
        $params = ['domain' => $site['domain'], 'user' => $site['system_user'], 'root' => $site['root'], 'id' => $b['backup_id'], 'files' => $files, 'databases' => (object) $map, 'safety_backup' => $files];
        if ($b['storage_id'] !== null) {
            $params['remote'] = $this->storageConfig((int) $b['storage_id']);
        }
        $res = (array) $this->agent->call('backup.restore', $params);
        $this->audit->record('backup.restore', 'website', (string) $site['id'], AuditLogger::SUCCESS, ['domain' => $site['domain'], 'backup' => $b['backup_id'], 'files' => $files, 'databases' => array_keys($map)]);
        return (string) $res['job_id'];
    }

    /**
     * First chunk of a stored part (for streaming downloads).
     *
     * @param array<string, mixed> $b
     * @return array{0: array<string, mixed>, 1: callable(int): array<string, mixed>}
     */
    public function download(array $b, string $file): array
    {
        $names = array_map(static fn (array $p): string => (string) $p['name'], $b['parts']);
        if (!in_array($file, $names, true) || !$b['local']) {
            throw new UserFacingException('That file is not stored on this server.', $b['local'] ? '' : 'This backup is kept in remote storage only; download it from there.');
        }
        $params = $this->target($b) + ['file' => $file];
        $next = fn (int $offset): array => (array) $this->agent->call('backup.download', $params + ['offset' => $offset], 120);
        $first = $next(0);
        $this->audit->record('backup.download', $b['kind'] === 'server' ? 'server' : 'website', (string) ($b['website_id'] ?? $b['server_id']), AuditLogger::SUCCESS, ['backup' => $b['backup_id'], 'file' => $file]);
        return [$first, $next];
    }

    /** @return array<string, mixed>|null */
    public function keyInfo(): ?array
    {
        try {
            return (array) $this->agent->call('backup.key_info');
        } catch (AgentException) {
            return null;
        }
    }
}
