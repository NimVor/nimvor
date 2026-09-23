<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Security\Authorizer;
use WhaCP\Security\AuthService;
use WhaCP\Services\UserService;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

final class ProfileController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly UserService $users,
        private readonly AuthService $auth,
        private readonly Authorizer $authorizer,
    ) {
        parent::__construct($view, $session);
    }

    public function show(Request $request): Response
    {
        $user = $this->user($request);
        return $this->render('profile/show', [
            'profile' => $user,
            'permissions' => $this->authorizer->permissions((int) $user['role_id']),
        ]);
    }

    public function password(Request $request): Response
    {
        $id = $this->userId($request);
        try {
            $this->users->changeOwnPassword(
                $id,
                (string) $request->input('current_password', ''),
                (string) $request->input('password', ''),
                (string) $request->input('password_confirmation', ''),
            );
        } catch (ValidationException $e) {
            return $this->backWithErrors('/profile', $e, []);
        }
        // Keep this session valid; every other session is invalidated.
        $this->auth->refreshFingerprint($id);
        return $this->redirectWithSuccess('/profile', 'Password changed. Other sessions have been signed out.');
    }
}
