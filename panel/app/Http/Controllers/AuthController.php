<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Core\Config;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Security\AuthService;
use WhaCP\Security\Csrf;
use WhaCP\View\View;

final class AuthController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AuthService $auth,
        private readonly Csrf $csrf,
        private readonly Config $config,
    ) {
        parent::__construct($view, $session);
    }

    public function show(Request $request): Response
    {
        if ($this->auth->user() !== null) {
            return Response::redirect('/');
        }
        return Response::html($this->view->render('auth/login', [
            'username' => (string) ($this->view->shared('old')['username'] ?? ''),
        ], 'layouts/minimal'));
    }

    public function login(Request $request): Response
    {
        $username = mb_substr($request->string('username'), 0, 190);
        $password = (string) $request->input('password', '');
        /** @var list<string> $proxies */
        $proxies = (array) $this->config->get('security.trusted_proxies', []);
        $error = $this->auth->attempt($username, is_string($password) ? $password : '', $request->ip($proxies));
        if ($error !== null) {
            $this->session->flash('error', $error);
            $this->session->flash('old', ['username' => $username]);
            return Response::redirect('/login');
        }
        if ($this->auth->pendingTwoFactor()) {
            return Response::redirect('/login/two-factor');
        }
        $this->csrf->rotate();
        return Response::redirect('/');
    }

    public function twoFactor(Request $request): Response
    {
        if (!$this->auth->pendingTwoFactor()) {
            return Response::redirect('/login');
        }
        return Response::html($this->view->render('auth/two_factor', [], 'layouts/minimal'));
    }

    public function verifyTwoFactor(Request $request): Response
    {
        if ($request->input('cancel') === '1') {
            $this->auth->cancelTwoFactor();
            return Response::redirect('/login');
        }
        /** @var list<string> $proxies */
        $proxies = (array) $this->config->get('security.trusted_proxies', []);
        $code = $request->input('code', '');
        $error = $this->auth->completeTwoFactor(is_string($code) ? mb_substr($code, 0, 40) : '', $request->ip($proxies));
        if ($error !== null) {
            $this->session->flash('error', $error);
            return Response::redirect($this->auth->pendingTwoFactor() ? '/login/two-factor' : '/login');
        }
        $this->csrf->rotate();
        return Response::redirect('/');
    }

    public function logout(Request $request): Response
    {
        $this->auth->logout();
        $this->session->flash('success', 'You have been signed out.');
        return Response::redirect('/login');
    }
}
