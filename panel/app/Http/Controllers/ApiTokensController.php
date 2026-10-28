<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\ApiTokenService;
use NimVor\Support\ValidationException;
use NimVor\View\View;

final class ApiTokensController extends Controller
{
    public function __construct(View $view, SessionInterface $session, private readonly ApiTokenService $tokens)
    {
        parent::__construct($view, $session);
    }

    public function store(Request $request): Response
    {
        try {
            $token = $this->tokens->create($this->userId($request), $request->all());
        } catch (ValidationException $e) {
            return $this->backWithErrors('/profile', $e, $request->all());
        }
        $this->session->flash('api_token', $token);
        return $this->redirectWithSuccess('/profile', 'Token created. Copy it now: it is shown only once.');
    }

    public function revoke(Request $request, string $id): Response
    {
        $this->tokens->revoke($this->userId($request), (int) $id);
        return $this->redirectWithSuccess('/profile', 'Token revoked.');
    }
}
