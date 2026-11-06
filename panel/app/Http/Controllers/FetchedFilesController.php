<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\FetchedFileService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * Backups > Remote fetched files: fetch a file from a remote SFTP or FTPS
 * server onto this server, list the fetched files and delete them (one or
 * several at once). Administrators only.
 */
final class FetchedFilesController extends Controller
{
    private const TO = '/backups#fetched';

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly FetchedFileService $service,
    ) {
        parent::__construct($view, $session);
    }

    private function denied(Request $request): bool
    {
        return !Scope::isAll($this->user($request));
    }

    /** The server's key, for the person to confirm before the login is sent (JSON). */
    public function probe(Request $request): Response
    {
        if ($this->denied($request)) {
            return Response::json(['success' => false, 'error' => ['message' => 'Only administrators can fetch remote files.']], 403);
        }
        try {
            return Response::json(['success' => true, 'data' => $this->service->probe($request->all())]);
        } catch (ValidationException $e) {
            return Response::json(['success' => false, 'error' => ['message' => $e->getMessage(), 'fields' => $e->errors()]], 422);
        } catch (UserFacingException $e) {
            return Response::json(['success' => false, 'error' => ['message' => trim($e->getMessage() . ' ' . $e->reason())]], 422);
        }
    }

    public function fetch(Request $request): Response
    {
        if ($this->denied($request)) {
            return $this->redirectWithError('/backups', 'Only administrators can fetch remote files.');
        }
        try {
            $res = $this->service->fetch($request->all(), $this->userId($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors(self::TO, $e, $request->all() + ['fetch_form' => '1']);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::TO, $e);
        }
        $this->flashJob($res['job']);
        return $this->redirectWithSuccess(self::TO, 'Fetching the file has started. It appears here as completed when the transfer is done.');
    }

    /** Delete one or several fetched files (the record and the file). */
    public function delete(Request $request): Response
    {
        if ($this->denied($request)) {
            return $this->redirectWithError('/backups', 'Only administrators can delete fetched files.');
        }
        $ids = $request->input('ids', []);
        try {
            $res = $this->service->delete(is_array($ids) ? array_map('intval', $ids) : [(int) $ids]);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::TO, $e);
        }
        $parts = [];
        if ($res['deleted'] > 0) {
            $parts[] = $res['deleted'] === 1 ? '1 fetched file was deleted.' : $res['deleted'] . ' fetched files were deleted.';
        }
        if ($res['running'] > 0) {
            $parts[] = ($res['running'] === 1 ? '1 file is' : $res['running'] . ' files are') . ' still being fetched and was left alone.';
        }
        if ($res['failed'] !== []) {
            $parts[] = 'Not deleted: ' . implode('; ', array_map(static fn (string $n, string $why): string => $n . ' (' . $why . ')', array_keys($res['failed']), $res['failed'])) . '.';
        }
        $message = implode(' ', $parts) ?: 'Nothing was deleted.';
        return $res['failed'] === [] && $res['deleted'] > 0
            ? $this->redirectWithSuccess(self::TO, $message)
            : $this->redirectWithError(self::TO, $message);
    }
}
