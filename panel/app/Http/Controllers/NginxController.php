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

    public function test(Request $request): Response
    {
        try {
            $r = $this->nginx->test();
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/nginx', $e);
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
            return $this->redirectWithFailure('/nginx', $e);
        }
        return $this->redirectWithSuccess('/nginx', 'Nginx reloaded.');
    }

    public function tuning(Request $request): Response
    {
        try {
            $this->nginx->applyTuning($request->all(), $this->userId($request));
        } catch (ValidationException $e) {
            return $this->redirectWithError('/nginx', implode(' ', $e->errors()));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/nginx', $e);
        }
        return $this->redirectWithSuccess('/nginx', 'Nginx tuning applied.');
    }
}
