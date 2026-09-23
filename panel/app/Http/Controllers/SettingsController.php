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
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        return $this->render('settings/index', [
            'definitions' => SettingsService::definitions(),
            'values' => $this->settings->all(),
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
}
