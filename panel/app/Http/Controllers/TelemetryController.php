<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\TelemetryService;
use NimVor\View\View;

/** Settings → Telemetry: the opt-in choice, what would be sent, turning it off, unregistering and deleting. */
final class TelemetryController extends Controller
{
    public function __construct(View $view, SessionInterface $session, private readonly TelemetryService $telemetry)
    {
        parent::__construct($view, $session);
    }

    public function show(Request $request): Response
    {
        return $this->render('settings/telemetry', ['status' => $this->telemetry->status(), 'preview' => $this->telemetry->preview()]);
    }

    public function save(Request $request): Response
    {
        $action = (string) $request->input('action', '');
        $user = $this->userId($request);
        switch ($action) {
            case 'enable':
                $this->telemetry->enable($user);
                $this->telemetry->run();
                return $this->redirectWithSuccess('/settings/telemetry', 'Telemetry is on. The first report goes out with the next scheduled pass.');
            case 'disable':
                $this->telemetry->disable($user);
                return $this->redirectWithSuccess('/settings/telemetry', 'Telemetry is off. Nothing more is sent.');
            case 'unregister':
                $this->telemetry->unregister(false, $user);
                return $this->redirectWithSuccess('/settings/telemetry', 'Telemetry is off and the installation was unregistered. What was sent before stays as anonymous totals.');
            case 'delete':
                $this->telemetry->unregister(true, $user);
                return $this->redirectWithSuccess('/settings/telemetry', 'Telemetry is off and everything stored about this installation is being deleted at the central service.');
        }
        return $this->redirectWithError('/settings/telemetry', 'Choose what to do.');
    }
}
