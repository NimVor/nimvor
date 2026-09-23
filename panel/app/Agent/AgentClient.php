<?php

declare(strict_types=1);

namespace WhaCP\Agent;

use WhaCP\Support\Logger;

/**
 * Calls named actions on the WhaCP agent.
 *
 * The panel never runs privileged commands itself; everything that touches
 * the host goes through an explicit agent action.
 */
class AgentClient
{
    private const ACTION_PATTERN = '/^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$/';

    private ?string $token = null;

    public function __construct(
        private readonly AgentTransport $transport,
        private readonly string $tokenFile,
        private readonly Logger $logger,
        private readonly int $timeoutSeconds = 30,
    ) {
    }

    /** @return array<string, mixed> */
    public function health(): array
    {
        return $this->decode('health', $this->transport->request('GET', '/v1/health', null, $this->token(), 5));
    }

    /**
     * Execute an action and return its data.
     *
     * @param array<string, mixed> $params
     */
    public function call(string $action, array $params = []): mixed
    {
        if (!preg_match(self::ACTION_PATTERN, $action)) {
            throw new \InvalidArgumentException("Invalid agent action name {$action}");
        }
        $body = $params === [] ? '{}' : json_encode($params, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);
        $started = microtime(true);
        try {
            $response = $this->transport->request('POST', '/v1/actions/' . $action, $body, $this->token(), $this->timeoutSeconds);
        } catch (AgentException $e) {
            $this->logger->warning('agent unreachable', ['action' => $action, 'code' => $e->errorCode(), 'reason' => $e->reason()]);
            throw $e;
        }
        $data = $this->decode($action, $response);
        $this->logger->debug('agent action', ['action' => $action, 'ms' => (int) ((microtime(true) - $started) * 1000)]);
        return $data['data'] ?? null;
    }

    /** @return array<string, mixed> */
    private function decode(string $action, AgentHttpResponse $response): array
    {
        $json = json_decode($response->body, true);
        if (!is_array($json)) {
            $this->logger->error('agent returned invalid JSON', ['action' => $action, 'status' => $response->status]);
            throw new AgentException(
                'The WhaCP agent returned an unexpected response.',
                'The response could not be decoded.',
                'Make sure the panel and agent versions match.',
                'agent_bad_response',
            );
        }
        if (($json['success'] ?? false) === true) {
            return $json;
        }
        $code = (string) ($json['error']['code'] ?? 'agent_error');
        $message = (string) ($json['error']['message'] ?? 'The agent reported an error.');
        $this->logger->warning('agent action failed', ['action' => $action, 'status' => $response->status, 'code' => $code, 'request_id' => $json['request_id'] ?? '']);

        $details = (string) ($json['error']['details'] ?? '');
        $status = $response->status >= 400 ? $response->status : 502;
        // Operational errors are already written for humans by the agent.
        if (in_array($code, ['failed', 'conflict', 'invalid_params', 'not_found'], true)) {
            throw new AgentException($message, '', '', $code, $status, null, $details);
        }
        [$title, $remedy] = match ($code) {
            'unauthorized' => ['The panel could not authenticate with the WhaCP agent.', 'Verify that the panel and agent use the same token file and that the panel user may read it.'],
            'write_disabled' => ['This server is in read-only mode.', 'The agent runs in read-only mode; enable write mode (see docs/installation, "Enabling write mode") to manage websites, databases and services.'],
            'unknown_action' => ['The agent does not support this action.', 'Upgrade the agent so that it matches the panel version.'],
            'timeout' => ['The action took too long to complete.', 'Try again; if it keeps failing, check the agent log.'],
            default => ['The WhaCP agent could not complete the request.', 'Check the agent log (journalctl -u whacp-agent) for request ' . ($json['request_id'] ?? '') . '.'],
        };
        throw new AgentException($title, $message, $remedy, $code, $status, null, $details);
    }

    private function token(): string
    {
        if ($this->token !== null) {
            return $this->token;
        }
        $raw = is_readable($this->tokenFile) ? @file_get_contents($this->tokenFile) : false;
        if ($raw === false || trim($raw) === '') {
            throw new AgentUnavailableException(
                'The panel could not authenticate with the WhaCP agent.',
                'The agent token file is missing or not readable by the panel.',
                'Run the installer again or check the permissions of the configured token file.',
                'agent_token_missing',
            );
        }
        return $this->token = trim($raw);
    }
}
