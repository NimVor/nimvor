<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentClient;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\ServerService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

final class ServersController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly ServerService $servers,
        private readonly AgentClient $agent,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        return $this->render('servers/index', ['servers' => $this->servers->all()]);
    }

    public function store(Request $request): Response
    {
        try {
            $id = $this->servers->add($request->all());
        } catch (ValidationException $e) {
            return $this->backWithErrors('/servers', $e, array_diff_key($request->all(), ['token' => 1]));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/servers', $e);
        }
        return $this->redirectWithSuccess('/servers', 'Server connected (id ' . $id . '). Switch to it with the selector at the top.');
    }

    public function check(Request $request, string $id): Response
    {
        $ok = $this->servers->check((int) $id, $this->agent);
        return $ok ? $this->redirectWithSuccess('/servers', 'The agent answered.') : $this->redirectWithError('/servers', 'The agent did not answer; see the error in the list.');
    }

    public function destroy(Request $request, string $id): Response
    {
        try {
            $this->servers->remove((int) $id);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/servers', $e);
        }
        return $this->redirectWithSuccess('/servers', 'Server removed from NimVor. Nothing was changed on it.');
    }

    public function switch(Request $request): Response
    {
        try {
            $this->servers->switchTo((int) $request->input('server_id', 1), $this->user($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/', $e);
        }
        return Response::redirect('/');
    }
}
