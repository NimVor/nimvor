<?php

declare(strict_types=1);

namespace NimVor\Services;

use DateTimeImmutable;
use DateTimeZone;
use NimVor\Database\Connection;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;

/**
 * Backup schedules and retention. `bin/nimvor schedule:run` (a systemd
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
            ? $this->db->fetchAll("SELECT sc.*, st.name AS storage_name FROM backup_schedules sc LEFT JOIN backup_storages st ON st.id = sc.storage_id WHERE sc.server_id = ? AND (sc.kind = 'server' OR sc.all_sites = 1) ORDER BY sc.id", [$this->server->id()])
            : $this->db->fetchAll('SELECT sc.*, st.name AS storage_name FROM backup_schedules sc LEFT JOIN backup_storages st ON st.id = sc.storage_id WHERE sc.website_id = ? ORDER BY sc.id', [(int) $site['id']]);
    }

    /** Websites of every account backed up by an all-websites schedule at once. */
    public const ALL_SITES_PARALLEL = 2;

    /**
     * @param array<string, mixed>|null $site
     * @param array<string, mixed> $input with $site null, scope "all_sites"
     *        backs up every website (each its own backup) instead of the
     *        server configuration
     */
    public function create(?array $site, array $input): int
    {
        $errors = [];
        $all = $site === null && ($input['scope'] ?? '') === 'all_sites';
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
        if (($site !== null || $all) && !$files && !$dbs) {
            $errors['include_files'] = 'Back up the files, the databases or both.';
        }
        if ($errors !== []) {
            throw new ValidationException($errors);
        }
        $this->db->run(
            'INSERT INTO backup_schedules (server_id, website_id, kind, all_sites, frequency, hour, weekday, include_files, include_databases, encrypt, storage_id, keep_local, retention, enabled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)',
            [$this->server->id(), $site === null ? null : (int) $site['id'], $site === null && !$all ? 'server' : 'site', $all ? 1 : 0, $freq, $hour, $weekday, $files ? 1 : 0, $dbs ? 1 : 0,
                filter_var($input['encrypt'] ?? true, FILTER_VALIDATE_BOOL) || $site === null ? 1 : 0, $storage,
                $storage === null ? 1 : (filter_var($input['keep_local'] ?? true, FILTER_VALIDATE_BOOL) ? 1 : 0), $retention, gmdate('c')]
        );
        $id = $this->db->lastInsertId();
        $this->audit->record('backup.schedule_create', $site === null ? 'server' : 'website', (string) ($site['id'] ?? $this->server->id()), AuditLogger::SUCCESS, ['frequency' => $freq, 'hour' => $hour, 'retention' => $retention] + ($all ? ['scope' => 'all_sites'] : []));
        return $id;
    }

    /** @param array<string, mixed>|null $site */
    public function delete(?array $site, int $id): void
    {
        $n = $site === null
            ? $this->db->run("DELETE FROM backup_schedules WHERE id = ? AND server_id = ? AND (kind = 'server' OR all_sites = 1)", [$id, $this->server->id()])->rowCount()
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
            if ((int) ($s['all_sites'] ?? 0) === 1) {
                $log = array_merge($log, $this->runAllSites($s, $now));
                continue;
            }
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
     * An all-websites schedule: when due, a round starts; each pass then
     * starts the backups of the websites not yet backed up in this round,
     * ALL_SITES_PARALLEL at a time, until every website has one.
     *
     * @param array<string, mixed> $s
     * @return list<string>
     */
    private function runAllSites(array $s, DateTimeImmutable $now): array
    {
        $id = (int) $s['id'];
        if (self::isDue($s, $now)) {
            $s['last_run_at'] = $now->format(DATE_ATOM);
            $s['last_status'] = 'running';
            $this->db->run("UPDATE backup_schedules SET last_run_at = ?, last_status = 'running' WHERE id = ?", [$s['last_run_at'], $id]);
        }
        if ($s['last_run_at'] === null || !str_starts_with((string) $s['last_status'], 'running')) {
            return [];
        }
        $since = (new DateTimeImmutable((string) $s['last_run_at']))->setTimezone(new DateTimeZone('UTC'))->format('c');
        $running = (int) $this->db->scalar("SELECT COUNT(*) FROM backups WHERE schedule_id = ? AND status = 'running'", [$id]);
        // Websites without a backup of this round (started, finished or failed).
        $todo = $this->db->fetchAll(
            'SELECT w.id FROM websites w WHERE w.server_id = ? AND NOT EXISTS (SELECT 1 FROM backups b WHERE b.schedule_id = ? AND b.website_id = w.id AND b.created_at >= ?) ORDER BY w.id',
            [$this->server->id(), $id, $since]
        );
        if ($todo === [] && $running === 0) {
            $round = $this->db->fetchAll('SELECT status, COUNT(*) AS n FROM backups WHERE schedule_id = ? AND created_at >= ? GROUP BY status', [$id, $since]);
            $by = array_column($round, 'n', 'status');
            $failed = (int) ($by['failed'] ?? 0);
            $this->db->run('UPDATE backup_schedules SET last_status = ? WHERE id = ?', [sprintf('%d website(s) backed up%s', (int) ($by['completed'] ?? 0), $failed > 0 ? ", {$failed} failed" : ''), $id]);
            if ($failed > 0) {
                $this->notifications->notify('schedule:' . $id . ':' . substr($since, 0, 16), 'job', 'error', 'Scheduled backup of every website: ' . $failed . ' failed', ['link' => '/backups', 'body' => 'See the backups of the websites concerned.']);
            }
            return ["All-websites schedule {$id}: round finished."];
        }
        $log = [];
        $opts = [
            'files' => (int) $s['include_files'] === 1, 'databases' => (int) $s['include_databases'] === 1,
            'encrypt' => (int) $s['encrypt'] === 1, 'storage_id' => $s['storage_id'] === null ? null : (int) $s['storage_id'],
            'keep_local' => (int) $s['keep_local'] === 1, 'label' => ucfirst((string) $s['frequency']) . ' backup (every website)', 'schedule_id' => $id,
        ];
        if ($opts['storage_id'] !== null && !$this->backups->storageActive((int) $opts['storage_id'])) {
            $opts['storage_id'] = null;
            $opts['keep_local'] = true;
        }
        foreach (array_slice($todo, 0, max(0, self::ALL_SITES_PARALLEL - $running)) as $row) {
            $site = $this->sites->find($this->server->id(), (int) $row['id'], null);
            if ($site === null) {
                continue;
            }
            try {
                $job = $this->backups->createSite($site, $opts, null);
                $this->jobs->track($job, 'Scheduled backup of ' . $site['domain'], 'website', (string) $site['id'], null);
                $log[] = "Started backup of {$site['domain']} (job {$job}).";
            } catch (\Throwable $e) {
                // Recorded as a failed backup of this round, so the round moves on.
                $this->db->run(
                    "INSERT INTO backups (server_id, website_id, schedule_id, kind, domain, label, status, error, created_at, finished_at) VALUES (?, ?, ?, 'site', ?, ?, 'failed', ?, ?, ?)",
                    [$this->server->id(), (int) $site['id'], $id, (string) $site['domain'], $opts['label'], mb_substr($e->getMessage(), 0, 500), gmdate('c'), gmdate('c')]
                );
                $log[] = "Backup of {$site['domain']} could not start: " . $e->getMessage();
            }
        }
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
            // An all-websites schedule keeps its retention per website.
            $old = (int) ($s['all_sites'] ?? 0) === 1
                ? $this->db->fetchAll(
                    "SELECT id, website_id FROM (SELECT id, website_id, ROW_NUMBER() OVER (PARTITION BY website_id ORDER BY id DESC) AS n FROM backups WHERE schedule_id = ? AND status = 'completed') WHERE n > ?",
                    [(int) $s['id'], (int) $s['retention']]
                )
                : $this->db->fetchAll(
                    "SELECT id, ? AS website_id FROM backups WHERE schedule_id = ? AND status = 'completed' ORDER BY id DESC LIMIT -1 OFFSET ?",
                    [$s['website_id'], (int) $s['id'], (int) $s['retention']]
                );
            foreach ($old as $row) {
                $site = $row['website_id'] === null ? null : $this->sites->find($this->server->id(), (int) $row['website_id'], null);
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
