<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\ServiceControlService;
use NimVor\View\View;

final class ServicesController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly ServiceControlService $control,
    ) {
        parent::__construct($view, $session);
    }

    public function control(Request $request): Response
    {
        $unit = $request->string('unit');
        $op = $request->string('op');
        // Pages that show service controls of their own.
        $back = in_array($request->string('return_to'), ['/mail/admin/server'], true) ? $request->string('return_to') : '/services';
        $json = $request->wantsJson(); // the dashboard's service widget
        if (!ServiceControlService::validUnit($unit) || !in_array($op, ServiceControlService::OPERATIONS, true)) {
            return $json ? Response::json(['success' => false, 'error' => ['message' => 'Invalid service operation.']], 422) : $this->redirectWithError($back, 'Invalid service operation.');
        }
        try {
            $res = $this->control->control($unit, $op);
        } catch (AgentException $e) {
            return $json ? Response::json(['success' => false, 'error' => $e->toArray()], 502) : $this->redirectWithFailure($back, $e);
        }
        return $json ? Response::json(['success' => true, 'message' => $res['message']]) : $this->redirectWithSuccess($back, $res['message']);
    }

    /** Restart one of NimVor's own services (agent and panel restart shortly after). */
    public function restartNimvor(Request $request, string $key): Response
    {
        if (!\NimVor\Hosting\Scope::isAll($this->user($request))) {
            return $this->redirectWithError('/services', 'Only server administrators can restart NimVor.');
        }
        try {
            $res = $this->control->restartNimvor($key);
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/services', $e);
        }
        return $this->redirectWithSuccess('/services', $res['scheduled']
            ? ($key === 'panel' ? 'The panel restarts in a few seconds; reload this page then.' : 'The agent restarts in a few seconds; running tasks finish first.')
            : 'Restarted ' . $res['unit'] . '.');
    }
}
