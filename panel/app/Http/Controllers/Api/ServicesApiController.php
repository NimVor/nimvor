<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Agent\AgentClient;
use NimVor\Hosting\Scope;
use NimVor\Http\Middleware\ApiAuthenticate;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\ServiceControlService;
use NimVor\Support\ValidationException;

/**
 * REST API v1: the server's services. Every endpoint calls ServiceControlService, as the web interface
 * does: the unit and the operation are validated, the agent only controls the services on its allowlist,
 * and every operation is audited. These endpoints need the services permissions, which hosting customers
 * do not have.
 */
final class ServicesApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly ServiceControlService $control,
        private readonly AgentClient $agent,
    ) {
    }

    /**
     * @param array<string, mixed> $u
     * @return array<string, mixed>
     */
    private static function unitOut(array $u): array
    {
        return [
            'unit' => (string) ($u['unit'] ?? ''), 'description' => (string) ($u['description'] ?? ''),
            'active_state' => (string) ($u['active_state'] ?? ''), 'sub_state' => (string) ($u['sub_state'] ?? ''),
            'enabled' => (string) ($u['unit_file_state'] ?? ''),
        ];
    }

    public function list(Request $r): Response
    {
        return $this->guard(fn (): Response => self::ok(array_values(array_map(
            static fn ($u): array => self::unitOut((array) $u),
            (array) $this->agent->call('services.status')
        ))));
    }

    public function nimvor(Request $r): Response
    {
        return $this->guard(fn (): Response => self::ok((array) json_decode((string) json_encode($this->agent->call('nimvor.services')), true)));
    }

    public function control(Request $r): Response
    {
        return $this->guard(function () use ($r): Response {
            $in = self::body($r);
            $res = $this->control->control((string) ($in['unit'] ?? ''), (string) ($in['op'] ?? ''));
            return self::ok(['unit' => (string) $in['unit'], 'op' => (string) $in['op'], 'scheduled' => $res['scheduled'], 'message' => $res['message']], $res['scheduled'] ? 202 : 200);
        });
    }

    public function restartNimvor(Request $r, string $key): Response
    {
        if (!Scope::isAll($this->user($r))) {
            return ApiAuthenticate::error(403, 'forbidden', 'Only server administrators can restart NimVor.');
        }
        return $this->guard(function () use ($key): Response {
            if (!preg_match('/^[a-z]{2,20}$/', $key)) {
                throw new ValidationException(['key' => 'Use agent, panel, scheduler, greylist, dkim or ftpd.']);
            }
            $res = $this->control->restartNimvor($key);
            return self::ok($res, $res['scheduled'] ? 202 : 200);
        });
    }
}
