<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\BackupScheduler;
use NimVor\Services\BackupService;
use NimVor\Services\FeatureService;
use NimVor\Services\JobTracker;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;

/**
 * REST API v1: a website's backups, their restore and their schedules. Every endpoint calls BackupService
 * or BackupScheduler, as the web interface does. A restore needs the website's domain as confirmation and
 * takes a safety backup of the current files first. Scheduled copies are hidden from accounts that may not
 * see them, and schedules are set up by administrators only.
 */
final class BackupsApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly BackupService $backups,
        private readonly BackupScheduler $scheduler,
        private readonly FeatureService $features,
        private readonly JobTracker $jobs,
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
     * @param array<string, mixed> $b
     * @return array<string, mixed>
     */
    private static function backupOut(array $b): array
    {
        return [
            'id' => (int) $b['id'], 'backup_id' => (string) $b['backup_id'], 'label' => (string) $b['label'], 'status' => (string) $b['status'],
            'size' => (int) $b['size'], 'encrypted' => (bool) $b['encrypted'], 'scheduled' => !empty($b['schedule_id']),
            'on_server' => (bool) $b['local'], 'storage_id' => $b['storage_id'] === null ? null : (int) $b['storage_id'],
            'parts' => array_values(array_map(static fn (array $p): array => [
                'name' => (string) ($p['name'] ?? ''), 'kind' => (string) ($p['kind'] ?? ''), 'database' => isset($p['database']) ? (string) $p['database'] : null, 'size' => (int) ($p['size'] ?? 0),
            ], (array) $b['parts'])),
            'created_at' => $b['created_at'], 'finished_at' => $b['finished_at'] ?? null, 'error' => $b['error'] ?? null,
        ];
    }

    /**
     * @param array<string, mixed> $s
     * @return array<string, mixed>
     */
    private static function scheduleOut(array $s): array
    {
        return [
            'id' => (int) $s['id'], 'frequency' => (string) $s['frequency'], 'hour' => (int) $s['hour'], 'weekday' => (int) $s['weekday'],
            'retention' => (int) $s['retention'], 'include_files' => (bool) $s['include_files'], 'include_databases' => (bool) $s['include_databases'],
            'encrypt' => (bool) $s['encrypt'], 'storage_id' => $s['storage_id'] === null ? null : (int) $s['storage_id'], 'keep_local' => (bool) $s['keep_local'],
            'enabled' => (bool) $s['enabled'], 'last_run_at' => $s['last_run_at'] ?? null,
        ];
    }

    /**
     * The backup, unless this account may not see scheduled copies.
     *
     * @param array<string, mixed> $s
     * @return array<string, mixed>|null
     */
    private function backup(Request $r, array $s, string $backup): ?array
    {
        $b = $this->backups->find($s, (int) $backup);
        return $b === null || (!empty($b['schedule_id']) && $this->features->hidesAutoBackups($this->user($r))) ? null : $b;
    }

    public function showBackup(Request $r, string $id, string $backup): Response
    {
        $s = $this->site($r, $id);
        $b = $s === null ? null : $this->backup($r, $s, $backup);
        return $b === null ? self::notFound() : self::ok(self::backupOut($b));
    }

    public function restoreBackup(Request $r, string $id, string $backup): Response
    {
        $s = $this->site($r, $id);
        $b = $s === null ? null : $this->backup($r, $s, $backup);
        if ($s === null || $b === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s, $b): Response {
            $in = self::body($r);
            $dbs = array_values(array_filter(array_map('strval', (array) ($in['databases'] ?? []))));
            $job = $this->backups->restore($s, $b, filter_var($in['files'] ?? false, FILTER_VALIDATE_BOOL), $dbs, (string) ($in['confirm'] ?? ''), (int) $this->user($r)['id']);
            $this->jobs->track($job, 'Restore ' . $s['domain'], 'website', (string) $s['id'], (int) $this->user($r)['id']);
            return self::ok(['job_id' => $job], 202);
        });
    }

    public function deleteBackup(Request $r, string $id, string $backup): Response
    {
        $s = $this->site($r, $id);
        $b = $s === null ? null : $this->backup($r, $s, $backup);
        if ($s === null || $b === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($b): Response {
            $this->backups->delete($b);
            return self::ok(['deleted' => (int) $b['id']]);
        });
    }

    public function listSchedules(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        // Accounts that may not see scheduled backups do not see the schedules either.
        $hide = $this->features->hidesAutoBackups($this->user($r));
        return self::ok($hide ? [] : array_map([self::class, 'scheduleOut'], $this->scheduler->list($s)));
    }

    public function createSchedule(Request $r, string $id): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s): Response {
            $this->assertAdministrator($r);
            $before = array_column($this->scheduler->list($s), 'id');
            $this->scheduler->create($s, self::body($r));
            $new = array_values(array_filter($this->scheduler->list($s), static fn (array $x): bool => !in_array($x['id'], $before, true)));
            return self::ok(self::scheduleOut((array) $new[0]), 201);
        });
    }

    public function deleteSchedule(Request $r, string $id, string $schedule): Response
    {
        $s = $this->site($r, $id);
        if ($s === null) {
            return self::notFound();
        }
        return $this->guard(function () use ($r, $s, $schedule): Response {
            $this->assertAdministrator($r);
            if (!in_array((int) $schedule, array_map('intval', array_column($this->scheduler->list($s), 'id')), true)) {
                return self::notFound();
            }
            $this->scheduler->delete($s, (int) $schedule);
            return self::ok(['deleted' => (int) $schedule]);
        });
    }

    private function assertAdministrator(Request $r): void
    {
        if (!Scope::isAll($this->user($r))) {
            throw new UserFacingException('Automatic backups are set up by your administrator.', '', '', 403);
        }
    }
}
