<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

abstract class Controller
{
    public function __construct(
        protected readonly View $view,
        protected readonly SessionInterface $session,
    ) {
    }

    /** @param array<string, mixed> $data */
    protected function render(string $template, array $data = [], int $status = 200): Response
    {
        return Response::html($this->view->render($template, $data), $status);
    }

    /** @return array<string, mixed> */
    protected function user(Request $request): array
    {
        /** @var array<string, mixed> */
        return $request->attribute('user', []);
    }

    protected function userId(Request $request): int
    {
        return (int) ($this->user($request)['id'] ?? 0);
    }

    protected function redirectWithSuccess(string $to, string $message): Response
    {
        $this->session->flash('success', $message);
        return Response::redirect($to);
    }

    protected function redirectWithError(string $to, string $message): Response
    {
        $this->session->flash('error', $message);
        return Response::redirect($to);
    }

    /**
     * Redirect with a user-facing error (and technical details from the
     * agent, e.g. nginx -t output, when available).
     */
    protected function redirectWithFailure(string $to, \WhaCP\Support\UserFacingException $e): Response
    {
        $msg = $e->getMessage();
        if ($e->reason() !== '') {
            $msg .= ' ' . $e->reason();
        }
        if ($e->remedy() !== '') {
            $msg .= ' ' . $e->remedy();
        }
        $this->session->flash('error', $msg);
        if ($e instanceof \WhaCP\Agent\AgentException && $e->details() !== '') {
            $this->session->flash('details', $e->details());
        }
        return Response::redirect($to);
    }

    protected function flashJob(string $jobId): void
    {
        $this->session->flash('job', $jobId);
    }

    /**
     * Return a MongoDB export as a downloadable attachment. The filename is
     * built from the database and collection with any unsafe characters
     * stripped, so it can never inject into the Content-Disposition header.
     *
     * @param array{content: string, count: int, format: string} $out
     */
    protected function downloadExport(string $db, string $collection, array $out): Response
    {
        $ext = $out['format'] === 'jsonl' ? 'jsonl' : 'json';
        $type = $out['format'] === 'jsonl' ? 'application/x-ndjson' : 'application/json';
        $base = preg_replace('/[^A-Za-z0-9._-]+/', '_', $db . '.' . $collection) ?? 'export';
        $name = trim($base, '_.') ?: 'export';
        return new Response($out['content'], 200, [
            'Content-Type' => $type . '; charset=utf-8',
            'Content-Disposition' => 'attachment; filename="' . $name . '.' . $ext . '"',
            'Cache-Control' => 'no-store',
        ]);
    }

    /**
     * Redirect back to a form with validation errors and the old input
     * (passwords are never flashed back).
     *
     * @param array<string, mixed> $input
     */
    protected function backWithErrors(string $to, ValidationException $e, array $input): Response
    {
        unset($input['password'], $input['password_confirmation'], $input['current_password'], $input['_token'], $input['secret_key'], $input['private_key'], $input['client_secret'], $input['sftp_password']);
        $this->session->flash('errors', $e->errors());
        $this->session->flash('old', $input);
        $this->session->flash('error', $e->getMessage());
        return Response::redirect($to);
    }
}
