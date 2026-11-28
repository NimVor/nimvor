<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\NginxService;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * Server → Nginx: service state, configuration test/reload and the
 * server-wide tuning. Administrators only (server.view / server.manage).
 */
final class NginxController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly NginxService $nginx,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $status = null;
        $error = null;
        try {
            $status = $this->nginx->status();
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('server/nginx', ['tab' => 'nginx', 'status' => $status, 'tuning' => $this->nginx->tuning(), 'agentError' => $error]);
    }

    /** Nginx in a window (the Nginx chip on the websites list): Start / Stop at the top, then state and tuning. */
    public function modal(Request $request): Response
    {
        $status = null;
        $error = null;
        try {
            $status = $this->nginx->status();
        } catch (AgentException $e) {
            $error = $e;
        }
        // The version is shown in the window's title; it is only a label, so it must look like one.
        $v = $request->query('v');
        $version = is_string($v) && preg_match('/^[0-9][0-9A-Za-z.+_-]{0,30}$/', $v) === 1 ? $v : '';
        return Response::html($this->view->partial('server/modal_nginx', ['status' => $status, 'tuning' => $this->nginx->tuning(), 'version' => $version, 'agentError' => $error]));
    }

    public function test(Request $request): Response
    {
        try {
            $r = $this->nginx->test();
        } catch (AgentException $e) {
            return $request->wantsJson() ? Response::json(['success' => false, 'error' => $e->toArray()], 502) : $this->redirectWithFailure('/nginx', $e);
        }
        if ($request->wantsJson()) {
            return $r['ok']
                ? Response::json(['success' => true, 'message' => 'The Nginx configuration is valid.'])
                : Response::json(['success' => false, 'error' => ['message' => 'The configuration is invalid: ' . $r['output']]], 422);
        }
        return $r['ok']
            ? $this->redirectWithSuccess('/nginx', 'The Nginx configuration is valid.')
            : $this->redirectWithError('/nginx', 'The configuration is invalid: ' . $r['output']);
    }

    public function reload(Request $request): Response
    {
        try {
            $this->nginx->reload($this->userId($request));
        } catch (AgentException $e) {
            return $request->wantsJson() ? Response::json(['success' => false, 'error' => $e->toArray()], 502) : $this->redirectWithFailure('/nginx', $e);
        }
        return $request->wantsJson() ? Response::json(['success' => true, 'message' => 'Nginx reloaded.']) : $this->redirectWithSuccess('/nginx', 'Nginx reloaded.');
    }

    public function tuning(Request $request): Response
    {
        try {
            $brotliActive = $this->nginx->applyTuning($request->all(), $this->userId($request));
        } catch (ValidationException $e) {
            return $request->wantsJson() ? Response::json(['success' => false, 'error' => ['message' => implode(' ', $e->errors()), 'errors' => $e->errors()]], 422) : $this->redirectWithError('/nginx', implode(' ', $e->errors()));
        } catch (AgentException $e) {
            return $request->wantsJson() ? Response::json(['success' => false, 'error' => $e->toArray()], 502) : $this->redirectWithFailure('/nginx', $e);
        }
        if ($request->wantsJson()) {
            return Response::json(['success' => true, 'message' => ($request->all()['brotli'] ?? '') === '1' && !$brotliActive ? 'Nginx tuning applied. Brotli is turned on but not active yet: its Nginx module is not installed.' : 'Nginx tuning applied.']);
        }
        if (($request->all()['brotli'] ?? '') === '1' && !$brotliActive) {
            return $this->redirectWithSuccess('/nginx', 'Nginx tuning applied. Brotli is turned on but not active yet: the Nginx Brotli module is not installed. Install it on the Packages page, then press Apply again.');
        }
        return $this->redirectWithSuccess('/nginx', 'Nginx tuning applied.');
    }
}
