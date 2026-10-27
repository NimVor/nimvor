<?php

declare(strict_types=1);

namespace WhaCP\Agent;

/**
 * HTTP over the agent's Unix socket using ext-curl.
 */
final class UnixSocketTransport implements AgentTransport
{
    private const MAX_RESPONSE = 32 * 1024 * 1024;

    public function __construct(private readonly string $socketPath)
    {
    }

    public function request(string $method, string $path, ?string $body, string $token, int $timeoutSeconds): AgentHttpResponse
    {
        if (!file_exists($this->socketPath)) {
            throw new AgentUnavailableException(
                'The NimVor agent is not reachable.',
                'The agent socket does not exist; the agent service is probably stopped.',
                'Start the agent with "systemctl start whacp-agent" and check "journalctl -u whacp-agent".',
                'agent_unavailable',
            );
        }
        $ch = curl_init('http://agent' . $path);
        $received = '';
        curl_setopt_array($ch, [
            CURLOPT_UNIX_SOCKET_PATH => $this->socketPath,
            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_HTTPHEADER => array_filter([
                'Authorization: Bearer ' . $token,
                'Accept: application/json',
                $body !== null ? 'Content-Type: application/json' : null,
            ]),
            CURLOPT_POSTFIELDS => $body,
            CURLOPT_CONNECTTIMEOUT => 3,
            CURLOPT_TIMEOUT => $timeoutSeconds,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_PROTOCOLS => CURLPROTO_HTTP,
            CURLOPT_WRITEFUNCTION => static function ($ch, string $chunk) use (&$received): int {
                if (strlen($received) + strlen($chunk) > self::MAX_RESPONSE) {
                    return 0; // abort oversized responses
                }
                $received .= $chunk;
                return strlen($chunk);
            },
        ]);
        $ok = curl_exec($ch);
        $status = (int) curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
        $errno = curl_errno($ch);

        if ($ok === false || $errno !== 0) {
            $timeout = $errno === CURLE_OPERATION_TIMEDOUT;
            throw new AgentUnavailableException(
                $timeout ? 'The NimVor agent did not respond in time.' : 'The NimVor agent is not reachable.',
                $timeout ? 'The request exceeded the configured timeout.' : 'The connection to the agent socket failed (curl error ' . $errno . ').',
                'Check that whacp-agent is running and that the panel user belongs to the agent socket group.',
                $timeout ? 'agent_timeout' : 'agent_unavailable',
            );
        }
        return new AgentHttpResponse($status, $received);
    }
}
