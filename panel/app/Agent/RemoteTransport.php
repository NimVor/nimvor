<?php

declare(strict_types=1);

namespace NimVor\Agent;

/**
 * HTTPS to a remote agent. The agent's self-signed certificate is not
 * checked against a CA; instead its public key must match the pin
 * recorded when the server was added (CURLOPT_PINNEDPUBLICKEY), which curl
 * enforces during the handshake — before the request (and the token) is
 * sent.
 */
final class RemoteTransport implements AgentTransport
{
    private const MAX_RESPONSE = 32 * 1024 * 1024;

    public function __construct(private readonly string $baseUrl, private readonly string $pin)
    {
    }

    public static function validUrl(string $url): bool
    {
        return (bool) preg_match('#^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$#', $url) || (bool) preg_match('#^https://\[[0-9a-fA-F:]+\](:[0-9]{1,5})?$#', $url);
    }

    public static function validPin(string $pin): bool
    {
        return (bool) preg_match('#^sha256//[A-Za-z0-9+/]{43}=$#', $pin);
    }

    public function request(string $method, string $path, ?string $body, string $token, int $timeoutSeconds): AgentHttpResponse
    {
        if (!self::validUrl($this->baseUrl) || !self::validPin($this->pin)) {
            throw new AgentUnavailableException('The remote server is not configured correctly.', 'Its address or certificate pin is invalid.', 'Edit the server under Servers.', 'agent_unavailable');
        }
        $ch = curl_init($this->baseUrl . $path);
        $received = '';
        curl_setopt_array($ch, [
            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_HTTPHEADER => array_filter([
                'Authorization: Bearer ' . $token,
                'Accept: application/json',
                $body !== null ? 'Content-Type: application/json' : null,
            ]),
            CURLOPT_POSTFIELDS => $body,
            CURLOPT_CONNECTTIMEOUT => 5,
            CURLOPT_TIMEOUT => $timeoutSeconds,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_PROTOCOLS => CURLPROTO_HTTPS,
            CURLOPT_SSL_VERIFYPEER => false, // self-signed; trust comes from the pin
            CURLOPT_SSL_VERIFYHOST => 0,
            CURLOPT_PINNEDPUBLICKEY => $this->pin,
            CURLOPT_SSLVERSION => CURL_SSLVERSION_TLSv1_3,
            CURLOPT_WRITEFUNCTION => static function ($ch, string $chunk) use (&$received): int {
                if (strlen($received) + strlen($chunk) > self::MAX_RESPONSE) {
                    return 0;
                }
                $received .= $chunk;
                return strlen($chunk);
            },
        ]);
        $ok = curl_exec($ch);
        $status = (int) curl_getinfo($ch, CURLINFO_RESPONSE_CODE);
        $errno = curl_errno($ch);
        if ($ok === false || $errno !== 0) {
            $pinFail = $errno === 90; // CURLE_SSL_PINNEDPUBKEYNOTMATCH
            $timeout = $errno === CURLE_OPERATION_TIMEDOUT;
            throw new AgentUnavailableException(
                $pinFail ? 'The remote server presented a different certificate.' : ($timeout ? 'The remote agent did not respond in time.' : 'The remote agent is not reachable.'),
                $pinFail ? 'Its TLS key does not match the pin recorded for it. Nothing was sent.' : 'The connection failed (curl error ' . $errno . ').',
                $pinFail ? 'If the agent was reinstalled, verify the new pin (nimvor-agent -pin on that server) and update it.' : 'Check that the agent listens on this address, the firewall allows the panel, and allowed_clients includes this server.',
                $pinFail ? 'agent_pin_mismatch' : ($timeout ? 'agent_timeout' : 'agent_unavailable'),
            );
        }
        $code = (string) (((array) json_decode($received, true))['error']['code'] ?? '');
        if ($status === 403 && $code === 'forbidden') {
            throw new AgentUnavailableException('The remote agent refused this panel.', 'The panel\'s address is not in allowed_clients, or it is locked out after failed logins.', 'Add this server\'s address to allowed_clients in /etc/nimvor/agent.json on the remote server.', 'agent_forbidden');
        }
        return new AgentHttpResponse($status, $received);
    }
}
