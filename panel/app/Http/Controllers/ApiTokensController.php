<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\ApiTokenService;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

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
