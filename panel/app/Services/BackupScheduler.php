<?php

declare(strict_types=1);

namespace WhaCP\Services;

use DateTimeImmutable;
use DateTimeZone;
use WhaCP\Database\Connection;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;

/**
 * Backup schedules and retention. `bin/whacp schedule:run` (a systemd
 * timer, every 5 minutes) starts due backups, records finished ones and
 * deletes the oldest completed backups beyond each schedule's retention.
 */
final class BackupScheduler
{
    public const WEEKDAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

    public function __construct(
        private readonly Connection $db,
        private readonly BackupService $backups,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
        private readonly JobTracker $jobs,
        private readonly NotificationService $notifications,
    ) {
    }

    /**
     * @param array<string, mixed>|null $site null = server schedules
     * @return list<array<string, mixed>>
     */
    public function list(?array $site): array
    {
        return $site === null
            ? $this->db->fetchAll("SELECT sc.*, st.name AS storage_name FROM backup_schedules sc LEFT JOIN backup_storages st ON st.id = sc.storage_id WHERE sc.server_id = ? AND sc.kind = 'server' ORDER BY sc.id", [$this->server->id()])
            : $this->db->fetchAll('SELECT sc.*, st.name AS storage_name FROM backup_schedules sc LEFT JOIN backup_storages st ON st.id = sc.storage_id WHERE sc.website_id = ? ORDER BY sc.id', [(int) $site['id']]);
    }

    /**
     * @param array<string, mixed>|null $site
     * @param array<string, mixed> $input
     */
    public function create(?array $site, array $input): int
    {
        $errors = [];
        $freq = (string) ($input['frequency'] ?? 'daily');
        if (!in_array($freq, ['daily', 'weekly'], true)) {
            $errors['frequency'] = 'Choose daily or weekly.';
        }
        $hour = (int) ($input['hour'] ?? 3);
        if ($hour < 0 || $hour > 23) {
            $errors['hour'] = 'Choose an hour between 0 and 23.';
        }
        $weekday = (int) ($input['weekday'] ?? 0);
        if ($weekday < 0 || $weekday > 6) {
            $errors['weekday'] = 'Choose a weekday.';
        }
        $retention = (int) ($input['retention'] ?? 7);
        if ($retention < 1 || $retention > 365) {
            $errors['retention'] = 'Keep between 1 and 365 backups.';
        }
        $storage = filter_var($input['storage_id'] ?? null, FILTER_VALIDATE_INT) ?: null;
        if ($storage !== null && $this->db->scalar('SELECT COUNT(*) FROM backup_storages WHERE id = ? AND active = 1', [$storage]) === 0) {
            $errors['storage_id'] = 'Choose a storage that is turned on.';
        }
        $files = filter_var($input['include_files'] ?? true, FILTER_VALIDATE_BOOL);
        $dbs = filter_var($input['include_databases'] ?? true, FILTER_VALIDATE_BOOL);
        if ($site !== null && !$files && !$dbs) {
            $errors['include_files'] = 'Back up the files, the databases or both.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $this->db->run(
            'INSERT INTO backup_schedules (server_id, website_id, kind, frequency, hour, weekday, include_files, include_databases, encrypt, storage_id, keep_local, retention, enabled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)',
            [$this->server->id(), $site === null ? null : (int) $site['id'], $site === null ? 'server' : 'site', $freq, $hour, $weekday, $files ? 1 : 0, $dbs ? 1 : 0,
                filter_var($input['encrypt'] ?? true, FILTER_VALIDATE_BOOL) || $site === null ? 1 : 0, $storage,
                $storage === null ? 1 : (filter_var($input['keep_local'] ?? true, FILTER_VALIDATE_BOOL) ? 1 : 0), $retention, gmdate('c')]
        );
        $id = $this->db->lastInsertId();
        $this->audit->record('backup.schedule_create', $site === null ? 'server' : 'website', (string) ($site['id'] ?? $this->server->id()), AuditLogger::SUCCESS, ['frequency' => $freq, 'hour' => $hour, 'retention' => $retention]);
        return $id;
    }

    /** @param array<string, mixed>|null $site */
    public function delete(?array $site, int $id): void
    {
        $n = $site === null
            ? $this->db->run("DELETE FROM backup_schedules WHERE id = ? AND server_id = ? AND kind = 'server'", [$id, $this->server->id()])->rowCount()
            : $this->db->run('DELETE FROM backup_schedules WHERE id = ? AND website_id = ?', [$id, (int) $site['id']])->rowCount();
        if ($n === 0) {
            throw new UserFacingException('Schedule not found.');
        }
        $this->audit->record('backup.schedule_delete', 'backup_schedule', (string) $id);
    }

    /** Most recent scheduled time at or before $now for a schedule. @param array<string, mixed> $s */
    public static function lastSlot(array $s, DateTimeImmutable $now): DateTimeImmutable
    {
        $slot = $now->setTime((int) $s['hour'], 0);
        if ($s['frequency'] === 'weekly') {
            $diff = ((int) $now->format('w') - (int) $s['weekday'] + 7) % 7;
            $slot = $slot->modify("-{$diff} days");
        }
        if ($slot > $now) {
            $slot = $slot->modify($s['frequency'] === 'weekly' ? '-7 days' : '-1 day');
        }
        return $slot;
    }

    /** @param array<string, mixed> $s */
    public static function isDue(array $s, DateTimeImmutable $now): bool
    {
        if ((int) $s['enabled'] !== 1) {
            return false;
        }
        $slot = self::lastSlot($s, $now);
        $last = $s['last_run_at'] === null ? null : new DateTimeImmutable((string) $s['last_run_at']);
        $created = new DateTimeImmutable((string) $s['created_at']);
        // A new schedule waits for its first slot instead of running at once.
        return ($last === null && $slot >= $created) || ($last !== null && $last < $slot);
    }

    /**
     * One scheduler pass. Returns log lines.
     *
     * @return list<string>
     */
    public function run(?DateTimeImmutable $now = null): array
    {
        $now ??= new DateTimeImmutable('now', new DateTimeZone(date_default_timezone_get()));
        $log = [];
        $this->backups->refresh();
        foreach ($this->db->fetchAll('SELECT * FROM backup_schedules WHERE server_id = ?', [$this->server->id()]) as $s) {
            if (!self::isDue($s, $now)) {
                continue;
            }
            $this->db->run('UPDATE backup_schedules SET last_run_at = ? WHERE id = ?', [$now->format(DATE_ATOM), (int) $s['id']]);
            $opts = [
                'files' => (int) $s['include_files'] === 1, 'databases' => (int) $s['include_databases'] === 1,
                'encrypt' => (int) $s['encrypt'] === 1, 'storage_id' => $s['storage_id'] === null ? null : (int) $s['storage_id'],
                'keep_local' => (int) $s['keep_local'] === 1, 'label' => ucfirst((string) $s['frequency']) . ' backup', 'schedule_id' => (int) $s['id'],
            ];
            // A storage turned off since: kept on this server only.
            $offStorage = $opts['storage_id'] !== null && !$this->backups->storageActive((int) $opts['storage_id']);
            if ($offStorage) {
                $opts['storage_id'] = null;
                $opts['keep_local'] = true;
            }
            try {
                if ($s['kind'] === 'server') {
                    $job = $this->backups->createServer($opts, null);
                    $what = 'server configuration';
                    $this->jobs->track($job, 'Scheduled backup of the server configuration', 'server', (string) $this->server->id(), null);
                } else {
                    $site = $this->sites->find($this->server->id(), (int) $s['website_id'], null) ?? throw new UserFacingException('website missing');
                    $job = $this->backups->createSite($site, $opts, null);
                    $what = (string) $site['domain'];
                    $this->jobs->track($job, 'Scheduled backup of ' . $what, 'website', (string) $site['id'], null);
                }
                $this->db->run("UPDATE backup_schedules SET last_status = 'started' WHERE id = ?", [(int) $s['id']]);
                $log[] = "Started backup of {$what} (job {$job})." . ($offStorage ? ' Its remote storage is turned off: kept on this server only.' : '');
            } catch (\Throwable $e) {
                $this->db->run('UPDATE backup_schedules SET last_status = ? WHERE id = ?', ['failed: ' . mb_substr($e->getMessage(), 0, 200), (int) $s['id']]);
                $this->audit->record('backup.scheduled', 'backup_schedule', (string) $s['id'], AuditLogger::FAILURE, ['error' => $e->getMessage()]);
                $siteId = $s['kind'] === 'server' ? null : (int) $s['website_id'];
                $this->notifications->notify('schedule:' . $s['id'] . ':' . $now->format('YmdHi'), 'job', 'error', 'Scheduled backup could not start', [
                    'website_id' => $siteId !== null && $this->db->scalar('SELECT COUNT(*) FROM websites WHERE id = ?', [$siteId]) > 0 ? $siteId : null,
                    'link' => $siteId !== null ? '/sites/' . $siteId . '/backups' : '/backups', 'body' => $e->getMessage(),
                ]);
                $log[] = "Schedule {$s['id']} failed: " . $e->getMessage();
            }
        }
        $log = array_merge($log, $this->prune());
        return $log;
    }

    /**
     * Delete completed backups beyond each schedule's retention.
     *
     * @return list<string>
     */
    public function prune(): array
    {
        $log = [];
        foreach ($this->db->fetchAll('SELECT * FROM backup_schedules WHERE server_id = ?', [$this->server->id()]) as $s) {
            $old = $this->db->fetchAll(
                "SELECT id FROM backups WHERE schedule_id = ? AND status = 'completed' ORDER BY id DESC LIMIT -1 OFFSET ?",
                [(int) $s['id'], (int) $s['retention']]
            );
            foreach ($old as $row) {
                $site = $s['website_id'] === null ? null : $this->sites->find($this->server->id(), (int) $s['website_id'], null);
                $b = $this->backups->find($site, (int) $row['id']);
                if ($b === null) {
                    continue;
                }
                try {
                    $this->backups->delete($b);
                    $log[] = 'Retention: deleted backup ' . $b['backup_id'] . '.';
                } catch (\Throwable $e) {
                    $log[] = 'Retention: could not delete backup ' . $b['backup_id'] . ': ' . $e->getMessage();
                }
            }
        }
        return $log;
    }
}
