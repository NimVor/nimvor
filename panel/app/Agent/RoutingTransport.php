<?php

declare(strict_types=1);

namespace NimVor\Agent;

use NimVor\Database\Connection;
use NimVor\Security\Crypto;
use NimVor\Services\ServerContext;

/**
 * Sends agent requests to the server selected in the ServerContext: the
 * local agent over its Unix socket, or a remote agent over pinned TLS
 * with that server's own token.
 */
final class RoutingTransport implements AgentTransport
{
    /** @var \Closure(string, string): AgentTransport */
    private \Closure $remoteFactory;

    public function __construct(
        private readonly AgentTransport $local,
        private readonly ServerContext $server,
        private readonly Connection $db,
        private readonly Crypto $crypto,
        ?\Closure $remoteFactory = null,
    ) {
        $this->remoteFactory = $remoteFactory ?? static fn (string $url, string $pin): AgentTransport => new RemoteTransport($url, $pin);
    }

    public function request(string $method, string $path, ?string $body, string $token, int $timeoutSeconds): AgentHttpResponse
    {
        $id = $this->server->id();
        if ($id === 1) {
            return $this->local->request($method, $path, $body, $token, $timeoutSeconds);
        }
        $row = $this->db->fetch("SELECT * FROM servers WHERE id = ? AND kind = 'remote'", [$id]);
        if ($row === null) {
            throw new AgentUnavailableException('The selected server no longer exists.', '', 'Choose another server.', 'agent_unavailable');
        }
        $remoteToken = $this->crypto->decrypt((string) $row['token_enc']);
        return ($this->remoteFactory)((string) $row['url'], (string) $row['fingerprint'])->request($method, $path, $body, $remoteToken, $timeoutSeconds);
    }
}
