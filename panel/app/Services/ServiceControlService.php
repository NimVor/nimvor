<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Support\ValidationException;

/**
 * Start, stop, restart, reload, enable and disable the server's services, and restart NimVor's own. The
 * web interface and the REST API both call this, so both validate the unit and the operation the same way,
 * leave the same audit record and let the agent's allowlist have the last word.
 */
final class ServiceControlService
{
    public const OPERATIONS = ['start', 'stop', 'restart', 'reload', 'enable', 'disable'];

    public function __construct(
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
    ) {
    }

    public static function validUnit(string $unit): bool
    {
        return preg_match('/^[A-Za-z0-9_][A-Za-z0-9@._:-]{0,199}\.service$/', $unit) === 1;
    }

    /**
     * @return array{scheduled: bool, message: string}
     * @throws ValidationException for an unknown unit name or operation
     * @throws AgentException when the agent refuses (the failure is audited)
     */
    public function control(string $unit, string $op): array
    {
        if (!self::validUnit($unit) || !in_array($op, self::OPERATIONS, true)) {
            throw new ValidationException(['unit' => 'Invalid service operation.']);
        }
        try {
            $res = (array) $this->agent->call('services.control', ['unit' => $unit, 'op' => $op]);
        } catch (AgentException $e) {
            $this->audit->record('service.' . $op, 'service', $unit, AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            throw $e;
        }
        $this->audit->record('service.' . $op, 'service', $unit);
        // Nginx carries the panel itself: its restart follows the answer by a moment.
        $scheduled = !empty($res['scheduled']);
        return [
            'scheduled' => $scheduled,
            'message' => $scheduled ? $unit . ' restarts in a few seconds; the panel may not answer for a moment.' : ucfirst($op) . ' of ' . $unit . ' completed.',
        ];
    }

    /**
     * Restart one of NimVor's own services (the agent and the panel restart shortly after answering).
     *
     * @return array{unit: string, scheduled: bool}
     */
    public function restartNimvor(string $key): array
    {
        try {
            $res = (array) $this->agent->call('nimvor.restart', ['key' => $key]);
        } catch (AgentException $e) {
            $this->audit->record('nimvor.restart', 'service', $key, AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            throw $e;
        }
        $this->audit->record('nimvor.restart', 'service', (string) ($res['unit'] ?? $key));
        return ['unit' => (string) ($res['unit'] ?? $key), 'scheduled' => !empty($res['scheduled'])];
    }
}
