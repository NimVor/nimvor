<?php

declare(strict_types=1);

namespace WhaCP\Agent;

interface AgentTransport
{
    /**
     * Perform an HTTP request against the agent.
     *
     * @throws AgentUnavailableException when the agent cannot be reached
     */
    public function request(string $method, string $path, ?string $body, string $token, int $timeoutSeconds): AgentHttpResponse;
}
