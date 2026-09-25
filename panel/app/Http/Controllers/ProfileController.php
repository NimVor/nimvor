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
    private const SETUP_KEY = '_2fa_setup_secret';

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly UserService $users,
        private readonly AuthService $auth,
        private readonly Authorizer $authorizer,
        private readonly \WhaCP\Services\ApiTokenService $tokens,
        private readonly \WhaCP\Security\TwoFactor $twoFactor,
        private readonly \WhaCP\Security\PasswordHasher $hasher,
        private readonly \WhaCP\Repositories\UserRepository $userRepo,
        private readonly \WhaCP\Services\AuditLogger $audit,
        private readonly \WhaCP\Security\WebAuthn $webAuthn,
        private readonly \WhaCP\Services\SettingsService $settings,
    ) {
        parent::__construct($view, $session);
    }

    public function show(Request $request): Response
    {
        $user = $this->user($request);
        return $this->render('profile/show', [
            'profile' => $user,
            'permissions' => $this->authorizer->permissions((int) $user['role_id']),
            'tokens' => $this->tokens->forUser((int) $user['id']),
            'newToken' => $this->session->pullFlash('api_token'),
            'twoFactor' => $this->twoFactor->status((int) $user['id']),
            'setupSecret' => $this->session->get(self::SETUP_KEY),
            'setupQr' => is_string($s = $this->session->get(self::SETUP_KEY))
                ? \WhaCP\Security\QrCode::encode(\WhaCP\Security\TwoFactor::uri($s, (string) $user['username']))->svg(4, 'QR code for your authenticator app')
                : null,
            'recoveryCodes' => $this->session->pullFlash('recovery_codes'),
            'securityKeys' => $this->webAuthn->credentials((int) $user['id']),
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

    private function passwordOk(int $id, Request $request): bool
    {
        $hash = $this->userRepo->passwordHash($id);
        $password = $request->input('current_password', '');
        return $hash !== null && is_string($password) && $this->hasher->verify($password, $hash);
    }

    public function twoFactorSetup(Request $request): Response
    {
        if ($this->twoFactor->enabled($this->userId($request))) {
            return $this->redirectWithError('/profile', 'Two-factor sign-in is already on.');
        }
        $this->session->set(self::SETUP_KEY, \WhaCP\Security\TwoFactor::newSecret());
        return Response::redirect('/profile#two-factor');
    }

    public function twoFactorEnable(Request $request): Response
    {
        $id = $this->userId($request);
        $secret = $this->session->get(self::SETUP_KEY);
        if (!is_string($secret)) {
            return $this->redirectWithError('/profile', 'Start the setup again.');
        }
        if (!$this->passwordOk($id, $request)) {
            return $this->redirectWithError('/profile#two-factor', 'Your current password is not correct.');
        }
        $code = $request->input('code', '');
        $codes = $this->twoFactor->enable($id, $secret, is_string($code) ? $code : '');
        if ($codes === null) {
            return $this->redirectWithError('/profile#two-factor', 'That code is not valid. Check that your phone shows the right time and try the newest code.');
        }
        $this->session->remove(self::SETUP_KEY);
        $this->session->flash('recovery_codes', $codes);
        $this->audit->record('user.two_factor_enabled', 'user', (string) $id, \WhaCP\Services\AuditLogger::SUCCESS);
        return $this->redirectWithSuccess('/profile#two-factor', 'Two-factor sign-in is on.');
    }

    public function twoFactorDisable(Request $request): Response
    {
        $id = $this->userId($request);
        if (!$this->passwordOk($id, $request)) {
            return $this->redirectWithError('/profile#two-factor', 'Your current password is not correct.');
        }
        $this->twoFactor->disable($id);
        $this->session->remove(self::SETUP_KEY);
        $this->audit->record('user.two_factor_disabled', 'user', (string) $id, \WhaCP\Services\AuditLogger::SUCCESS);
        return $this->redirectWithSuccess('/profile#two-factor', 'Two-factor sign-in is off.');
    }

    public function twoFactorRecovery(Request $request): Response
    {
        $id = $this->userId($request);
        if (!$this->twoFactor->enabled($id)) {
            return $this->redirectWithError('/profile', 'Two-factor sign-in is off.');
        }
        if (!$this->passwordOk($id, $request)) {
            return $this->redirectWithError('/profile#two-factor', 'Your current password is not correct.');
        }
        $this->session->flash('recovery_codes', $this->twoFactor->regenerateRecovery($id));
        $this->audit->record('user.two_factor_recovery', 'user', (string) $id, \WhaCP\Services\AuditLogger::SUCCESS);
        return $this->redirectWithSuccess('/profile#two-factor', 'New recovery codes created; the old ones no longer work.');
    }

    // ---- security keys (WebAuthn) ---------------------------------------

    public function keyOptions(Request $request): Response
    {
        $o = \WhaCP\Security\WebAuthnOrigin::of($request, $this->settings);
        $name = $this->settings->string('panel_name');
        return Response::json(['success' => true, 'data' => $this->webAuthn->registrationOptions($this->user($request), $o['rp_id'], $name !== '' ? $name : 'WhaCP')]);
    }

    public function keyAdd(Request $request): Response
    {
        $o = \WhaCP\Security\WebAuthnOrigin::of($request, $this->settings);
        try {
            $this->webAuthn->register($this->user($request), $o['origin'], $o['rp_id'], $request->string('name'), substr($request->string('client_data'), 0, 8192), substr($request->string('attestation'), 0, 16384));
        } catch (\WhaCP\Security\WebAuthnException $e) {
            return Response::json(['success' => false, 'error' => ['message' => $e->getMessage()]], 400);
        }
        $this->session->flash('success', 'Security key added. You can now use it to sign in.');
        return Response::json(['success' => true, 'data' => ['redirect' => '/profile#security-keys']]);
    }

    public function keyDelete(Request $request, string $id): Response
    {
        $uid = $this->userId($request);
        if (!$this->passwordOk($uid, $request)) {
            return $this->redirectWithError('/profile#security-keys', 'Your current password is not correct.');
        }
        if (!$this->webAuthn->delete($uid, (int) $id)) {
            return $this->redirectWithError('/profile#security-keys', 'Key not found.');
        }
        return $this->redirectWithSuccess('/profile#security-keys', 'Security key removed.');
    }
}
