<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\AccountBackupService;
use NimVor\Services\JobTracker;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * Account backups (Backups → Accounts): back up a whole hosting account to
 * one file, download or upload such files, and restore an account from one,
 * also after it was terminated. Administrators only.
 */
final class AccountBackupsController extends Controller
{
    private const SECRETS = 'account_restore_secrets';

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AccountBackupService $service,
        private readonly JobTracker $jobs,
        private readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    private function denied(Request $request): ?Response
    {
        return Scope::isAll($this->user($request)) ? null : $this->redirectWithError('/backups', 'Only administrators manage account backups.');
    }

    public function create(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $userId = (int) $request->input('user_id', 0);
        try {
            $job = $this->service->create($userId, $request->string('label'), $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/backups#accounts', $e);
        }
        $this->jobs->track($job, 'Account backup', 'user', (string) $userId, $this->userId($request));
        $this->flashJob($job);
        return $this->redirectWithSuccess('/backups#accounts', 'Account backup started: every website is backed up, then the account file is written.');
    }

    public function download(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $path = (string) $request->query('path', '');
        try {
            $first = $this->service->chunk($path, 0);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/backups#accounts', $e);
        }
        $service = $this->service;
        $name = preg_replace('/[^A-Za-z0-9._-]/', '_', basename($path)) ?: 'account.nimvor-account';
        return Response::stream(static function () use ($first, $service, $path): void {
            if (session_status() === PHP_SESSION_ACTIVE) {
                session_write_close();
            }
            $chunk = $first;
            $offset = 0;
            while (true) {
                $bytes = base64_decode($chunk['data'], true) ?: '';
                echo $bytes;
                flush();
                $offset += strlen($bytes);
                if ($chunk['eof'] || $bytes === '') {
                    break;
                }
                $chunk = $service->chunk($path, $offset);
            }
        }, [
            'Content-Type' => 'application/octet-stream',
            'Content-Disposition' => 'attachment; filename="' . $name . '"',
            'Content-Length' => (string) $first['size'],
            'X-Content-Type-Options' => 'nosniff',
            'Cache-Control' => 'no-store',
        ]);
    }

    public function delete(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        try {
            $this->service->delete($request->string('path'), $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/backups#accounts', $e);
        }
        return $this->redirectWithSuccess('/backups#accounts', 'Account backup file deleted.');
    }

    /** One chunk of an uploaded account backup file (JSON). */
    public function upload(Request $request): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return Response::json(['success' => false, 'error' => ['message' => 'Only administrators upload account backups.']], 403);
        }
        $chunk = $_FILES['chunk'] ?? null;
        if (!is_array($chunk) || ($chunk['error'] ?? 1) !== UPLOAD_ERR_OK || !is_uploaded_file((string) $chunk['tmp_name'])) {
            return Response::json(['success' => false, 'error' => ['message' => 'Upload failed.']], 400);
        }
        try {
            $res = $this->service->upload($request->string('name'), (int) $request->input('offset', 0), (string) file_get_contents((string) $chunk['tmp_name']), $request->input('final') === '1');
        } catch (UserFacingException $e) {
            return Response::json(['success' => false, 'error' => ['message' => trim($e->getMessage() . ' ' . $e->reason())]], 400);
        }
        return Response::json(['success' => true, 'data' => $res]);
    }

    /** What restoring a file does: the account, its websites and databases. */
    public function preview(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $path = (string) $request->query('path', '');
        try {
            $insp = $this->service->inspect($path);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/backups#accounts', $e);
        }
        return $this->render('backups/account_restore', ['path' => $path, 'manifest' => $insp['manifest'], 'plan' => $insp['plan'], 'restore' => null]);
    }

    public function start(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $path = $request->string('path');
        $manifest = null;
        try {
            $manifest = $this->service->inspect($path)['manifest'];
            if ($request->string('confirm') !== (string) $manifest['username']) {
                throw new UserFacingException('Type the account name to confirm the restore.');
            }
            $id = $this->service->start($path, $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($manifest !== null ? '/backups/accounts/restore?path=' . rawurlencode($path) : '/backups#accounts', $e);
        }
        $rec = $this->service->restoreRecord($id);
        if ($rec !== null) {
            $this->jobs->track((string) $rec['job_id'], 'Import account backup ' . $rec['username'], 'server', (string) $this->server->id(), $this->userId($request));
            $this->flashJob((string) $rec['job_id']);
        }
        return $this->redirectWithSuccess('/backups/accounts/restores/' . $id, 'Checking the file and importing its website backups.');
    }

    public function show(Request $request, string $id): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $rec = $this->service->restoreRecord((int) $id);
        if ($rec === null) {
            return $this->redirectWithError('/backups#accounts', 'The restore no longer exists.');
        }
        $state = $this->service->importState($rec);
        $rec = $this->service->restoreRecord((int) $id) ?? $rec;
        $secrets = $this->session->get(self::SECRETS);
        $this->session->remove(self::SECRETS);
        return $this->render('backups/account_restore', [
            'path' => (string) $rec['path'], 'manifest' => $rec['manifest'], 'plan' => $this->service->plan((array) ($rec['manifest']['meta'] ?? [])),
            'restore' => $rec, 'importState' => $state,
            'secrets' => is_array($secrets) && (int) ($secrets['id'] ?? 0) === (int) $id ? (array) $secrets['list'] : [],
        ]);
    }

    public function recreate(Request $request, string $id): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $to = '/backups/accounts/restores/' . (int) $id;
        try {
            $out = $this->service->recreate((int) $id, $this->user($request));
        } catch (ValidationException $e) {
            return $this->redirectWithError($to, implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        foreach ($out['jobs'] as $j) {
            $this->jobs->track($j['job'], 'Restore ' . $j['domain'], 'website', (string) $j['website_id'], $this->userId($request));
        }
        if ($out['jobs'] !== []) {
            $this->flashJob($out['jobs'][0]['job']);
        }
        // Shown once on the next page, never stored.
        $this->session->set(self::SECRETS, ['id' => (int) $id, 'list' => $out['secrets']]);
        return $this->redirectWithSuccess($to, 'Account recreated; its websites are being restored.');
    }
}
