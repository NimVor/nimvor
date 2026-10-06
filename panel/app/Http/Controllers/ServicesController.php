<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\AuditLogger;
use WhaCP\View\View;

final class ServicesController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
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
        if (!preg_match('/^[A-Za-z0-9_][A-Za-z0-9@._:-]{0,199}\.service$/', $unit) || !in_array($op, ['start', 'stop', 'restart', 'reload', 'enable', 'disable'], true)) {
            return $json ? Response::json(['success' => false, 'error' => ['message' => 'Invalid service operation.']], 422) : $this->redirectWithError($back, 'Invalid service operation.');
        }
        try {
            $this->agent->call('services.control', ['unit' => $unit, 'op' => $op]);
        } catch (AgentException $e) {
            $this->audit->record('service.' . $op, 'service', $unit, AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            return $json ? Response::json(['success' => false, 'error' => $e->toArray()], 502) : $this->redirectWithFailure($back, $e);
        }
        $this->audit->record('service.' . $op, 'service', $unit);
        $message = ucfirst($op) . ' of ' . $unit . ' completed.';
        return $json ? Response::json(['success' => true, 'message' => $message]) : $this->redirectWithSuccess($back, $message);
    }

    /** Restart one of WhaCP's own services (agent and panel restart shortly after). */
    public function restartWhacp(Request $request, string $key): Response
    {
        if (!\WhaCP\Hosting\Scope::isAll($this->user($request))) {
            return $this->redirectWithError('/services', 'Only server administrators can restart WhaCP.');
        }
        try {
            $res = (array) $this->agent->call('whacp.restart', ['key' => $key]);
        } catch (AgentException $e) {
            $this->audit->record('whacp.restart', 'service', $key, AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            return $this->redirectWithFailure('/services', $e);
        }
        $this->audit->record('whacp.restart', 'service', (string) ($res['unit'] ?? $key));
        return $this->redirectWithSuccess('/services', !empty($res['scheduled'])
            ? ($key === 'panel' ? 'The panel restarts in a few seconds; reload this page then.' : 'The agent restarts in a few seconds; running tasks finish first.')
            : 'Restarted ' . $res['unit'] . '.');
    }
}
