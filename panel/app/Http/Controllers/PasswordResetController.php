<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Core\Config;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Security\PasswordReset;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

final class PasswordResetController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly PasswordReset $resets,
        private readonly Config $config,
    ) {
        parent::__construct($view, $session);
    }

    public function forgot(Request $request): Response
    {
        return Response::html($this->view->render('auth/forgot', ['available' => $this->resets->available()], 'layouts/minimal'));
    }

    public function send(Request $request): Response
    {
        /** @var list<string> $proxies */
        $proxies = (array) $this->config->get('security.trusted_proxies', []);
        $this->resets->request($request->string('identifier'), $request->ip($proxies));
        $this->session->flash('success', 'If that account exists and has an email address, a link to reset the password is on its way. It is valid for 30 minutes.');
        return Response::redirect('/login');
    }

    public function show(Request $request, string $token): Response
    {
        $user = $this->resets->userFor($token);
        return Response::html($this->view->render('auth/reset', ['valid' => $user !== null, 'token' => $token], 'layouts/minimal'), $user === null ? 404 : 200);
    }

    public function reset(Request $request, string $token): Response
    {
        try {
            $this->resets->reset($token, (string) $request->input('password', ''), (string) $request->input('password_confirmation', ''));
        } catch (ValidationException $e) {
            $this->session->flash('errors', $e->errors());
            return Response::redirect('/password/reset/' . (preg_match('/^[a-f0-9]{64}$/', $token) ? $token : ''));
        }
        $this->session->flash('success', 'Your password was changed. Sign in with the new password.');
        return Response::redirect('/login');
    }
}
