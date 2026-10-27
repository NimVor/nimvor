<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\BackupScheduler;
use NimVor\Services\BackupService;
use NimVor\Services\JobTracker;
use NimVor\Services\ServerContext;
use NimVor\Services\SettingsService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

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
        private readonly SettingsService $settings,
        private readonly \NimVor\Repositories\UserRepository $users,
        private readonly \NimVor\Security\PasswordHasher $hasher,
        private readonly \NimVor\Services\AuditLogger $audit,
        private readonly \NimVor\Services\AccountBackupService $accountBackups,
        private readonly \NimVor\Services\FeatureService $features,
    ) {
        parent::__construct($view, $session, $sites, $server);
    }

    /**
     * A hosting account that is not allowed to see NimVor's scheduled
     * (automatic) backups. Those are the backup copies with a schedule_id.
     *
     * @param array<string, mixed> $user
     */
    private function hidesAutoBackups(array $user): bool
    {
        return ($user['role_scope'] ?? 'all') === 'own' && !$this->features->enabledFor($user, 'auto_backups');
    }

    /**
     * @param list<array<string, mixed>> $backups
     * @return list<array<string, mixed>>
     */
    private function withoutAuto(array $backups): array
    {
        return array_values(array_filter($backups, static fn (array $b): bool => empty($b['schedule_id'])));
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
        $user = $this->user($request);
        $backups = $this->backups->list($site);
        $schedules = $this->scheduler->list($site);
        if ($this->hidesAutoBackups($user)) {
            // Hide the scheduled copies and the schedule itself from the customer.
            $backups = $this->withoutAuto($backups);
            $schedules = [];
        }
        return $this->render('sites/backups', [
            'site' => $site,
            'backups' => $backups,
            'schedules' => $schedules,
            'storages' => Scope::isAll($this->user($request)) ? $this->backups->storages() : [],
            'databases' => $this->backups->siteDatabases($site),
            // Automatic backups are set up by administrators only.
            'canSchedule' => Scope::isAll($this->user($request)),
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
        $hideAuto = $this->hidesAutoBackups($this->user($request));
        return $this->act($request, $id, 'backups', function (array $site) use ($request, $backup, $hideAuto): array {
            $b = $this->backups->find($site, (int) $backup) ?? throw new UserFacingException('Backup not found.');
            if ($hideAuto && !empty($b['schedule_id'])) {
                throw new UserFacingException('Backup not found.');
            }
            $dbs = array_values(array_filter(array_map('strval', (array) $request->input('restore_databases', []))));
            $job = $this->backups->restore($site, $b, $request->input('restore_files') === '1', $dbs, $request->string('confirm_domain'), $this->userId($request));
            $this->jobs->track($job, 'Restore ' . $site['domain'], 'website', (string) $site['id'], $this->userId($request));
            return ['Restore started. A safety backup of the current files is taken first.', $job];
        });
    }

    public function delete(Request $request, string $id, string $backup): Response
    {
        $hideAuto = $this->hidesAutoBackups($this->user($request));
        return $this->act($request, $id, 'backups', function (array $site) use ($backup, $hideAuto): string {
            $b = $this->backups->find($site, (int) $backup) ?? throw new UserFacingException('Backup not found.');
            if ($hideAuto && !empty($b['schedule_id'])) {
                throw new UserFacingException('Backup not found.');
            }
            $this->backups->delete($b);
            return 'Backup deleted.';
        });
    }

    public function download(Request $request, string $id, string $backup): Response
    {
        $site = $this->site($request, $id);
        $b = $site === null ? null : $this->backups->find($site, (int) $backup);
        if ($b === null || ($this->hidesAutoBackups($this->user($request)) && !empty($b['schedule_id']))) {
            return Response::html('Not found', 404);
        }
        return $this->stream($b, (string) $request->query('file', ''), '/sites/' . (int) $id . '/backups');
    }

    public function storeSchedule(Request $request, string $id): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->redirectWithError('/sites/' . (int) $id . '/backups', 'Automatic backups are set up by your administrator.');
        }
        return $this->act($request, $id, 'backups', function (array $site) use ($request): string {
            $input = $request->all();
            $this->scheduler->create($site, $input);
            return 'Schedule added.';
        });
    }

    public function deleteSchedule(Request $request, string $id, string $schedule): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->redirectWithError('/sites/' . (int) $id . '/backups', 'Automatic backups are set up by your administrator.');
        }
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
        $customerBackups = $this->backups->listForUser($user);
        if ($this->hidesAutoBackups($user)) {
            $customerBackups = $this->withoutAuto($customerBackups);
        }
        return $this->render('backups/index', [
            'admin' => $admin,
            'backups' => $customerBackups,
            'serverBackups' => $admin ? $this->backups->list(null) : [],
            'schedules' => $admin ? $this->scheduler->list(null) : [],
            'storages' => $admin ? $this->backups->storages() : [],
            'keyInfo' => $admin ? $this->backups->keyInfo() : null,
            'panelUrl' => $this->settings->string('panel_url'),
            'accountFiles' => $admin ? $this->accountBackups->files() : [],
            'accounts' => $admin ? $this->accountBackups->accounts() : [],
            'isAdminRole' => ($user['role_name'] ?? '') === 'admin',
        ]);
    }

    /** @param callable(): (string|array{0: string, 1: string}) $fn */
    private function adminAct(Request $request, callable $fn, string $to = '/backups#server'): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->redirectWithError('/backups', 'Only administrators can manage server backups and storage.');
        }
        try {
            $res = $fn();
        } catch (ValidationException $e) {
            return $this->backWithErrors($to, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        if (is_array($res)) {
            $this->flashJob($res[1]);
            $res = $res[0];
        }
        return $this->redirectWithSuccess($to, $res);
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

    public function restoreServer(Request $request, string $backup): Response
    {
        return $this->adminAct($request, function () use ($request, $backup): array {
            $b = $this->backups->find(null, (int) $backup) ?? throw new UserFacingException('Backup not found.');
            $extract = $request->input('extract_only') === '1';
            $components = array_values(array_map('strval', (array) $request->input('components', [])));
            $job = $this->backups->restoreServer($b, $components, $extract, $request->string('confirm'));
            $this->jobs->track($job, $extract ? 'Extract server backup' : 'Restore server configuration', 'server', (string) $this->server->id(), $this->userId($request));
            return [$extract
                ? 'Extracting the backup for review; the job log shows where. Nothing on the server changes.'
                : 'Restore started. A safety backup of the current configuration is taken first; any invalid configuration is rolled back.', $job];
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

    private const PENDING = 'backup_storage_pending';

    /**
     * Add a storage: validate, then either test and save it, ask the
     * administrator to trust the server's host key or certificate first, or
     * (Google Drive) send them to Google's consent page.
     */
    public function storeStorage(Request $request): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->redirectWithError('/backups#storages', 'Only administrators can manage server backups and storage.');
        }
        try {
            $pending = $this->backups->buildStorage($request->all());
            if ($pending['cfg']['type'] === 'gdrive') {
                $base = rtrim($this->settings->string('panel_url'), '/');
                if (!preg_match('#^https://#', $base)) {
                    throw new UserFacingException('Google Drive needs the panel to be reachable over HTTPS.', 'Google only returns to https:// addresses.', 'Set the public panel URL in Settings to its https:// address and add this address as an authorised redirect URI in your Google Cloud OAuth client: ' . $this->backups->googleRedirectUri($base ?: 'https://panel.example.com'));
                }
                $state = bin2hex(random_bytes(16));
                $this->session->set(self::PENDING, ['state' => $state, 'sealed' => $this->backups->seal($pending), 'kind' => 'gdrive']);
                return Response::away($this->backups->googleAuthUrl($pending, $this->backups->googleRedirectUri($base), $state));
            }
            $trust = $this->backups->testStorage($pending['cfg']);
            if ($trust !== []) {
                $this->session->set(self::PENDING, ['sealed' => $this->backups->seal($pending), 'trust' => $trust, 'kind' => $pending['cfg']['type']]);
                return Response::redirect('/backups/storages/confirm');
            }
            $this->backups->insertStorage($pending['name'], $pending['cfg']);
        } catch (ValidationException $e) {
            return $this->backWithErrors('/backups#storages', $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/backups#storages', $e);
        }
        return $this->redirectWithSuccess('/backups#storages', 'Storage connected and tested.');
    }

    /** Show the host key / certificate the server presented. */
    public function confirmStorage(Request $request): Response
    {
        $p = $this->session->get(self::PENDING);
        if (!Scope::isAll($this->user($request)) || !is_array($p) || !isset($p['trust'])) {
            return Response::redirect('/backups#storages');
        }
        try {
            $pending = $this->backups->unseal($p['sealed'] ?? null);
        } catch (UserFacingException $e) {
            $this->session->remove(self::PENDING);
            return $this->redirectWithFailure('/backups#storages', $e);
        }
        return $this->render('backups/confirm_storage', [
            'name' => $pending['name'],
            'where' => BackupService::describe($pending['cfg']),
            'trust' => (array) $p['trust'],
        ]);
    }

    public function trustStorage(Request $request): Response
    {
        $p = $this->session->get(self::PENDING);
        $this->session->remove(self::PENDING);
        if (!Scope::isAll($this->user($request)) || !is_array($p) || !isset($p['trust'])) {
            return Response::redirect('/backups#storages');
        }
        if ($request->input('cancel') !== null) {
            return $this->redirectWithSuccess('/backups#storages', 'The storage was not added.');
        }
        try {
            $pending = $this->backups->unseal($p['sealed'] ?? null);
            // Test again with the key pinned: it must now connect.
            $this->backups->testStorage($pending['cfg']);
            $this->backups->insertStorage($pending['name'], $pending['cfg']);
        } catch (ValidationException $e) {
            return $this->backWithErrors('/backups#storages', $e, []);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/backups#storages', $e);
        }
        return $this->redirectWithSuccess('/backups#storages', 'Storage connected and tested.');
    }

    /** Google sends the administrator back here after the consent page. */
    public function googleCallback(Request $request): Response
    {
        $p = $this->session->get(self::PENDING);
        $this->session->remove(self::PENDING);
        if (!Scope::isAll($this->user($request)) || !is_array($p) || ($p['kind'] ?? '') !== 'gdrive') {
            return $this->redirectWithError('/backups#storages', 'No Google Drive connection is in progress. Add the storage again.');
        }
        if (!hash_equals((string) ($p['state'] ?? ''), (string) $request->query('state', ''))) {
            return $this->redirectWithError('/backups#storages', 'The Google Drive connection could not be verified (state mismatch). Add the storage again.');
        }
        $error = (string) $request->query('error', '');
        $code = (string) $request->query('code', '');
        if ($error !== '' || $code === '') {
            return $this->redirectWithError('/backups#storages', $error === 'access_denied' ? 'Google Drive access was not granted.' : 'Google did not return an authorisation code' . ($error !== '' ? ' (' . $error . ')' : '') . '.');
        }
        try {
            $pending = $this->backups->unseal($p['sealed'] ?? null);
            $this->backups->googleComplete($pending, $code, $this->backups->googleRedirectUri($this->settings->string('panel_url')));
        } catch (ValidationException $e) {
            return $this->backWithErrors('/backups#storages', $e, []);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/backups#storages', $e);
        }
        return $this->redirectWithSuccess('/backups#storages', 'Google Drive connected. Backups go to the "NimVor backups" folder.');
    }

    /** Find backups kept in a storage (recovery onto a new server). */
    public function scanStorage(Request $request, string $storage): Response
    {
        return $this->adminAct($request, function () use ($request, $storage): string {
            $r = $this->backups->findInStorage((int) $storage, $this->userId($request));
            $msg = sprintf('%d backup(s) added, %d already known.', $r['imported'], $r['known']);
            if ($r['no_website'] !== []) {
                $msg .= ' Create these websites first, then search again: ' . implode(', ', array_slice($r['no_website'], 0, 20)) . '.';
            }
            if ($r['skipped'] !== []) {
                $msg .= ' Not added (' . count($r['skipped']) . '): ' . implode('; ', array_slice($r['skipped'], 0, 5)) . (count($r['skipped']) > 5 ? '; …' : '') . '.';
            }
            return $msg;
        }, '/backups#storages');
    }

    public function deleteStorage(Request $request, string $storage): Response
    {
        return $this->adminAct($request, function () use ($storage): string {
            $this->backups->deleteStorage((int) $storage);
            return 'Storage removed.';
        }, '/backups#storages');
    }

    public function testStorage(Request $request, string $storage): Response
    {
        return $this->adminAct($request, function () use ($storage): string {
            $r = $this->backups->retestStorage((int) $storage);
            if (!$r['ok']) {
                throw new UserFacingException('The test failed: ' . $r['error'], 'The storage was turned off.', 'Check its settings (Edit) and test again.');
            }
            return 'The storage works: it can be turned on.';
        }, '/backups#storages');
    }

    public function activateStorage(Request $request, string $storage): Response
    {
        return $this->adminAct($request, function () use ($request, $storage): string {
            $on = $request->input('active') === '1';
            $this->backups->setStorageActive((int) $storage, $on);
            return $on ? 'Storage turned on: new backups can use it.' : 'Storage turned off: new backups do not use it (backups already there stay restorable).';
        }, '/backups#storages');
    }

    public function updateStorage(Request $request, string $storage): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->redirectWithError('/backups', 'Only administrators can manage server backups and storage.');
        }
        try {
            $this->backups->updateStorage((int) $storage, $request->all());
        } catch (ValidationException $e) {
            return $this->backWithErrors('/backups#storage-edit-' . (int) $storage, $e, $request->all() + ['storage_id' => (string) (int) $storage]);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/backups#storages', $e);
        }
        return $this->redirectWithSuccess('/backups#storages', 'Storage saved and tested.');
    }

    /**
     * The backup key, encrypted with a passphrase, as a file to keep safe.
     * Administrators only, after re-entering their panel password; every
     * download is in the audit log.
     */
    public function downloadKey(Request $request): Response
    {
        $user = $this->user($request);
        if (($user['role_name'] ?? '') !== 'admin') {
            return $this->redirectWithError('/backups#server', 'Only administrators can download the backup key.');
        }
        $hash = $this->users->passwordHash((int) $user['id']);
        if ($hash === null || !$this->hasher->verify((string) $request->input('current_password', ''), $hash)) {
            $this->audit->record('backup.key_export', 'server', (string) $this->server->id(), \NimVor\Services\AuditLogger::DENIED, ['reason' => 'wrong panel password']);
            return $this->redirectWithError('/backups#key-download', 'Your panel password is not correct.');
        }
        $pass = (string) $request->input('passphrase', '');
        if ($pass !== (string) $request->input('passphrase_confirmation', '')) {
            return $this->redirectWithError('/backups#key-download', 'The passphrases do not match.');
        }
        if (mb_strlen($pass) < 12) {
            return $this->redirectWithError('/backups#key-download', 'Use a passphrase of at least 12 characters.');
        }
        try {
            $res = $this->backups->exportKey($pass);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/backups#server', $e);
        }
        $name = 'nimvor-backup-key-' . preg_replace('/[^a-f0-9]/', '', $res['key_id']) . '.json';
        return new Response($res['file'], 200, [
            'Content-Type' => 'application/json',
            'Content-Disposition' => 'attachment; filename="' . $name . '"',
            'X-Content-Type-Options' => 'nosniff',
            'Cache-Control' => 'no-store',
        ]);
    }

    /** Install a backup key from a key file (a server without a key yet). */
    public function importKey(Request $request): Response
    {
        $user = $this->user($request);
        if (($user['role_name'] ?? '') !== 'admin') {
            return $this->redirectWithError('/backups#server', 'Only administrators can import a backup key.');
        }
        $file = $_FILES['key_file'] ?? null;
        if (!is_array($file) || ($file['error'] ?? 1) !== UPLOAD_ERR_OK || !is_uploaded_file((string) $file['tmp_name']) || (int) $file['size'] > 4096) {
            return $this->redirectWithError('/backups#key-import', 'Choose the key file (nimvor-backup-key-….json).');
        }
        try {
            $res = $this->backups->importKey((string) file_get_contents((string) $file['tmp_name']), (string) $request->input('passphrase', ''));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/backups#key-import', $e);
        }
        return $this->redirectWithSuccess('/backups#server', $res['already'] ? 'This server already has that backup key.' : 'Backup key ' . $res['key_id'] . ' installed: backups encrypted with it can be restored here.');
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
