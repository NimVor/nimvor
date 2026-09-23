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
        if (!preg_match('/^[A-Za-z0-9_][A-Za-z0-9@._:-]{0,199}\.service$/', $unit) || !in_array($op, ['start', 'stop', 'restart', 'reload', 'enable', 'disable'], true)) {
            return $this->redirectWithError('/services', 'Invalid service operation.');
        }
        try {
            $this->agent->call('services.control', ['unit' => $unit, 'op' => $op]);
        } catch (AgentException $e) {
            $this->audit->record('service.' . $op, 'service', $unit, AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            return $this->redirectWithFailure('/services', $e);
        }
        $this->audit->record('service.' . $op, 'service', $unit);
        return $this->redirectWithSuccess('/services', ucfirst($op) . ' of ' . $unit . ' completed.');
    }
}
