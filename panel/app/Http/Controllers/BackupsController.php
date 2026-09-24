<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\BackupScheduler;
use WhaCP\Services\BackupService;
use WhaCP\Services\JobTracker;
use WhaCP\Services\ServerContext;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

final class BackupsController extends SiteController
{
    public function __construct(
        View $view,
        SessionInterface $session,
        WebsiteRepository $sites,
        ServerContext $server,
        private readonly BackupService $backups,
        private readonly BackupScheduler $scheduler,
        private readonly JobTracker $jobs,
    ) {
        parent::__construct($view, $session, $sites, $server);
    }

    private function refresh(): void
    {
        try {
            $this->backups->refresh();
        } catch (AgentException) {
        }
    }

    // ---- Website tab ----------------------------------------------------

    public function show(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $this->refresh();
        return $this->render('sites/backups', [
            'site' => $site,
            'backups' => $this->backups->list($site),
            'schedules' => $this->scheduler->list($site),
            'storages' => Scope::isAll($this->user($request)) ? $this->backups->storages() : [],
            'databases' => $this->backups->siteDatabases($site),
        ]);
    }

    public function create(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'backups', function (array $site) use ($request): array {
            $job = $this->backups->createSite($site, $this->options($request) + [
                'files' => $request->input('files') === '1',
                'databases' => $request->input('databases') === '1',
            ], $this->userId($request));
            $this->jobs->track($job, 'Backup ' . $site['domain'], 'website', (string) $site['id'], $this->userId($request));
            return ['Backup started.', $job];
        });
    }

    /** @return array{encrypt: bool, storage_id: int|null, keep_local: bool, label: string} */
    private function options(Request $request): array
    {
        // Remote storage is configured by administrators for the whole server.
        $storage = Scope::isAll($this->user($request)) ? (filter_var($request->input('storage_id'), FILTER_VALIDATE_INT) ?: null) : null;
        return [
            'encrypt' => $request->input('encrypt', '1') === '1',
            'storage_id' => $storage,
            'keep_local' => $request->input('keep_local', '1') === '1',
            'label' => $request->string('label'),
        ];
    }

    public function restore(Request $request, string $id, string $backup): Response
    {
        return $this->act($request, $id, 'backups', function (array $site) use ($request, $backup): array {
            $b = $this->backups->find($site, (int) $backup) ?? throw new UserFacingException('Backup not found.');
            $dbs = array_values(array_filter(array_map('strval', (array) $request->input('restore_databases', []))));
            $job = $this->backups->restore($site, $b, $request->input('restore_files') === '1', $dbs, $request->string('confirm_domain'), $this->userId($request));
            $this->jobs->track($job, 'Restore ' . $site['domain'], 'website', (string) $site['id'], $this->userId($request));
            return ['Restore started. A safety backup of the current files is taken first.', $job];
        });
    }

    public function delete(Request $request, string $id, string $backup): Response
    {
        return $this->act($request, $id, 'backups', function (array $site) use ($backup): string {
            $b = $this->backups->find($site, (int) $backup) ?? throw new UserFacingException('Backup not found.');
            $this->backups->delete($b);
            return 'Backup deleted.';
        });
    }

    public function download(Request $request, string $id, string $backup): Response
    {
        $site = $this->site($request, $id);
        $b = $site === null ? null : $this->backups->find($site, (int) $backup);
        if ($b === null) {
            return Response::html('Not found', 404);
        }
        return $this->stream($b, (string) $request->query('file', ''), '/sites/' . (int) $id . '/backups');
    }

    public function storeSchedule(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'backups', function (array $site) use ($request): string {
            $input = $request->all();
            if (!Scope::isAll($this->user($request))) {
                unset($input['storage_id']);
            }
            $this->scheduler->create($site, $input);
            return 'Schedule added.';
        });
    }

    public function deleteSchedule(Request $request, string $id, string $schedule): Response
    {
        return $this->act($request, $id, 'backups', function (array $site) use ($schedule): string {
            $this->scheduler->delete($site, (int) $schedule);
            return 'Schedule removed. Existing backups are kept.';
        });
    }

    // ---- Server-wide page ----------------------------------------------

    public function index(Request $request): Response
    {
        $user = $this->user($request);
        $this->refresh();
        $admin = Scope::isAll($user);
        return $this->render('backups/index', [
            'admin' => $admin,
            'backups' => $this->backups->listForUser($user),
            'serverBackups' => $admin ? $this->backups->list(null) : [],
            'schedules' => $admin ? $this->scheduler->list(null) : [],
            'storages' => $admin ? $this->backups->storages() : [],
            'keyInfo' => $admin ? $this->backups->keyInfo() : null,
        ]);
    }

    /** @param callable(): (string|array{0: string, 1: string}) $fn */
    private function adminAct(Request $request, callable $fn): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->redirectWithError('/backups', 'Only administrators can manage server backups and storage.');
        }
        try {
            $res = $fn();
        } catch (ValidationException $e) {
            return $this->backWithErrors('/backups', $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/backups', $e);
        }
        if (is_array($res)) {
            $this->flashJob($res[1]);
            $res = $res[0];
        }
        return $this->redirectWithSuccess('/backups', $res);
    }

    public function createServer(Request $request): Response
    {
        return $this->adminAct($request, function () use ($request): array {
            $job = $this->backups->createServer($this->options($request), $this->userId($request));
            $this->jobs->track($job, 'Server configuration backup', 'server', (string) $this->server->id(), $this->userId($request));
            return ['Server configuration backup started.', $job];
        });
    }

    public function deleteServer(Request $request, string $backup): Response
    {
        return $this->adminAct($request, function () use ($backup): string {
            $b = $this->backups->find(null, (int) $backup) ?? throw new UserFacingException('Backup not found.');
            $this->backups->delete($b);
            return 'Backup deleted.';
        });
    }

    public function downloadServer(Request $request, string $backup): Response
    {
        $b = Scope::isAll($this->user($request)) ? $this->backups->find(null, (int) $backup) : null;
        if ($b === null) {
            return Response::html('Not found', 404);
        }
        return $this->stream($b, (string) $request->query('file', ''), '/backups');
    }

    public function storeStorage(Request $request): Response
    {
        return $this->adminAct($request, function () use ($request): string {
            $this->backups->saveStorage($request->all());
            return 'Storage connected and tested.';
        });
    }

    public function deleteStorage(Request $request, string $storage): Response
    {
        return $this->adminAct($request, function () use ($storage): string {
            $this->backups->deleteStorage((int) $storage);
            return 'Storage removed.';
        });
    }

    public function storeServerSchedule(Request $request): Response
    {
        return $this->adminAct($request, function () use ($request): string {
            $this->scheduler->create(null, $request->all());
            return 'Schedule added.';
        });
    }

    public function deleteServerSchedule(Request $request, string $schedule): Response
    {
        return $this->adminAct($request, function () use ($schedule): string {
            $this->scheduler->delete(null, (int) $schedule);
            return 'Schedule removed.';
        });
    }

    /** @param array<string, mixed> $b */
    private function stream(array $b, string $file, string $back): Response
    {
        try {
            [$first, $next] = $this->backups->download($b, $file);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($back, $e);
        }
        $name = preg_replace('/[^A-Za-z0-9._-]/', '_', ($b['domain'] !== '' ? $b['domain'] : 'server') . '-' . $b['backup_id'] . '-' . $file) ?: 'backup';
        return Response::stream(static function () use ($first, $next): void {
            if (session_status() === PHP_SESSION_ACTIVE) {
                session_write_close();
            }
            $chunk = $first;
            $offset = 0;
            while (true) {
                $bytes = base64_decode((string) ($chunk['data'] ?? ''), true) ?: '';
                echo $bytes;
                flush();
                $offset += strlen($bytes);
                if (!empty($chunk['eof']) || $bytes === '') {
                    break;
                }
                $chunk = $next($offset);
            }
        }, [
            'Content-Type' => 'application/octet-stream',
            'Content-Disposition' => 'attachment; filename="' . $name . '"',
            'Content-Length' => (string) (int) ($first['size'] ?? 0),
            'X-Content-Type-Options' => 'nosniff',
            'Cache-Control' => 'no-store',
        ]);
    }
}
