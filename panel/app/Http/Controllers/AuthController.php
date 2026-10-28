<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Core\Config;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Security\AuthService;
use NimVor\Security\Csrf;
use NimVor\View\View;

final class AuthController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AuthService $auth,
        private readonly Csrf $csrf,
        private readonly Config $config,
        private readonly \NimVor\Services\SettingsService $settings,
        private readonly \NimVor\Services\TerminalService $terminal,
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
        if ($this->auth->pendingIpVerification()) {
            return Response::redirect('/login/verify-ip');
        }
        $this->csrf->rotate();
        return Response::redirect('/');
    }

    public function verifyIpForm(Request $request): Response
    {
        if (!$this->auth->pendingIpVerification()) {
            return Response::redirect('/login');
        }
        return Response::html($this->view->render('auth/verify_ip', [], 'layouts/minimal'));
    }

    public function verifyIp(Request $request): Response
    {
        if ($request->input('cancel') === '1') {
            $this->auth->cancelIpVerification();
            return Response::redirect('/login');
        }
        /** @var list<string> $proxies */
        $proxies = (array) $this->config->get('security.trusted_proxies', []);
        $code = $request->input('code', '');
        $error = $this->auth->completeIpVerification(is_string($code) ? mb_substr($code, 0, 20) : '', $request->ip($proxies));
        if ($error !== null) {
            $this->session->flash('error', $error);
            return Response::redirect($this->auth->pendingIpVerification() ? '/login/verify-ip' : '/login');
        }
        $this->csrf->rotate();
        return Response::redirect('/');
    }

    public function twoFactor(Request $request): Response
    {
        if (!$this->auth->pendingTwoFactor()) {
            return Response::redirect('/login');
        }
        return Response::html($this->view->render('auth/two_factor', ['methods' => $this->auth->pendingMethods()], 'layouts/minimal'));
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
        if ($this->auth->pendingIpVerification()) {
            return Response::redirect('/login/verify-ip');
        }
        $this->csrf->rotate();
        return Response::redirect('/');
    }

    /** Options for the browser's security-key prompt (JSON). */
    public function securityKeyOptions(Request $request): Response
    {
        $o = \NimVor\Security\WebAuthnOrigin::of($request, $this->settings);
        $options = $this->auth->securityKeyOptions($o['rp_id']);
        if ($options === null) {
            return Response::json(['success' => false, 'error' => ['message' => 'Your sign-in expired. Enter your password again.']], 401);
        }
        return Response::json(['success' => true, 'data' => $options]);
    }

    /** Finish the sign-in with the security key's answer (JSON). */
    public function securityKey(Request $request): Response
    {
        /** @var list<string> $proxies */
        $proxies = (array) $this->config->get('security.trusted_proxies', []);
        $o = \NimVor\Security\WebAuthnOrigin::of($request, $this->settings);
        $error = $this->auth->completeWithSecurityKey($o['origin'], $o['rp_id'], $request->all(), $request->ip($proxies));
        if ($error !== null) {
            return Response::json(['success' => false, 'error' => ['message' => $error], 'restart' => !$this->auth->pendingTwoFactor()], 400);
        }
        if ($this->auth->pendingIpVerification()) {
            return Response::json(['success' => true, 'data' => ['redirect' => '/login/verify-ip']]);
        }
        $this->csrf->rotate();
        return Response::json(['success' => true, 'data' => ['redirect' => '/']]);
    }

    /** Step-up: confirm the password before a sensitive change. */
    public function confirmForm(Request $request): Response
    {
        return $this->render('auth/confirm_password', ['return' => \NimVor\Http\Middleware\Authenticate::safePath($request->string('return'))]);
    }

    public function confirm(Request $request): Response
    {
        /** @var list<string> $proxies */
        $proxies = (array) $this->config->get('security.trusted_proxies', []);
        $return = \NimVor\Http\Middleware\Authenticate::safePath($request->string('return'));
        $password = $request->input('password', '');
        if (!$this->auth->confirmPassword(is_string($password) ? $password : '', $request->ip($proxies))) {
            $this->session->flash('error', 'That password is not correct.');
            return Response::redirect('/confirm-password?return=' . rawurlencode($return));
        }
        $this->session->flash('success', 'Password confirmed for 15 minutes. Submit your change again.');
        return Response::redirect($return);
    }

    public function logout(Request $request): Response
    {
        // Web terminals of this sign-in end with it.
        $this->terminal->closeOwn();
        $this->auth->logout();
        $this->session->flash('success', 'You have been signed out.');
        return Response::redirect('/login');
    }
}
