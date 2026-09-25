<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Core\Config;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\SettingsService;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

final class SettingsController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly SettingsService $settings,
        private readonly Config $config,
        private readonly \WhaCP\Mail\MailConfig $mail,
        private readonly \WhaCP\Mail\Mailer $mailer,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        return $this->render('settings/index', [
            'definitions' => SettingsService::definitions(),
            'values' => $this->settings->all(),
            'mail' => $this->mail->get(),
            'system' => [
                'Panel version' => $this->config->string('app.version'),
                'PHP version' => PHP_VERSION,
                'Agent socket' => $this->config->string('agent.socket'),
                'Database' => 'SQLite',
                'Secure session cookie' => $this->config->bool('session.secure_cookie') ? 'Yes' : 'No (development only)',
            ],
        ]);
    }

    public function update(Request $request): Response
    {
        $input = $request->all();
        try {
            $this->settings->update($input, $this->userId($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors('/settings', $e, $input);
        }
        return $this->redirectWithSuccess('/settings', 'Settings saved.');
    }

    public function mail(Request $request): Response
    {
        $input = $request->all();
        unset($input['_token']);
        try {
            $this->mail->save($input, $this->userId($request));
        } catch (ValidationException $e) {
            unset($input['password']);
            return $this->backWithErrors('/settings#email', $e, $input);
        }
        return $this->redirectWithSuccess('/settings#email', 'Email settings saved. Send a test message to check them.');
    }

    public function testMail(Request $request): Response
    {
        $to = (string) ($this->user($request)['email'] ?? '');
        try {
            $this->mailer->send([$to], 'WhaCP test message', "This is a test message from WhaCP.\n\nIf you can read it, outgoing email works.");
        } catch (\WhaCP\Mail\MailException $e) {
            return $this->redirectWithFailure('/settings#email', $e);
        }
        return $this->redirectWithSuccess('/settings#email', 'Test message sent to ' . $to . '.');
    }
}
