<?php

declare(strict_types=1);

namespace NimVor\Tests\Support;

use NimVor\Agent\AgentHttpResponse;
use NimVor\Agent\AgentTransport;
use NimVor\Agent\AgentUnavailableException;

/**
 * Agent transport returning canned responses keyed by path.
 */
final class FakeTransport implements AgentTransport
{
    /** @var array<string, AgentHttpResponse> */
    public array $responses = [];

    /** @var list<array{method: string, path: string, body: ?string, token: string}> */
    public array $requests = [];

    public bool $down = false;

    /** When true, any action without an explicit response returns empty OK. */
    public bool $okByDefault = false;

    /** Make every unconfigured action return a generic success (empty data). */
    public function succeedAny(): void
    {
        $this->okByDefault = true;
    }

    /** @param array<string, mixed> $data */
    public function succeed(string $action, mixed $data): void
    {
        $this->responses['/v1/actions/' . $action] = new AgentHttpResponse(200, (string) json_encode([
            'success' => true, 'action' => $action, 'message' => 'OK', 'data' => $data, 'request_id' => 'test',
        ]));
    }

    public function fail(string $action, int $status, string $code, string $message): void
    {
        $this->responses['/v1/actions/' . $action] = new AgentHttpResponse($status, (string) json_encode([
            'success' => false, 'action' => $action, 'message' => $message, 'error' => ['code' => $code, 'message' => $message], 'request_id' => 'rid42',
        ]));
    }

    public function request(string $method, string $path, ?string $body, string $token, int $timeoutSeconds): AgentHttpResponse
    {
        $this->requests[] = ['method' => $method, 'path' => $path, 'body' => $body, 'token' => $token];
        if ($this->down) {
            throw new AgentUnavailableException('The NimVor agent is not reachable.', 'socket missing', 'start it', 'agent_unavailable');
        }
        if (isset($this->responses[$path])) {
            return $this->responses[$path];
        }
        if ($this->okByDefault && str_starts_with($path, '/v1/actions/')) {
            return new AgentHttpResponse(200, (string) json_encode([
                'success' => true, 'action' => substr($path, strlen('/v1/actions/')), 'message' => 'OK', 'data' => [], 'request_id' => 'test',
            ]));
        }
        return new AgentHttpResponse(404, (string) json_encode([
            'success' => false, 'error' => ['code' => 'unknown_action', 'message' => 'nope'],
        ]));
    }
}
