<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;

/**
 * Cached (per request) answer to "can this agent modify the server?".
 */
final class AgentStatus
{
    /** @var array<string, mixed>|false|null */
    private array|false|null $status = null;

    public function __construct(private readonly AgentClient $agent)
    {
    }

    /** @return array<string, mixed>|null null when the agent is unreachable */
    public function get(): ?array
    {
        if ($this->status === null) {
            try {
                $s = $this->agent->call('agent.status');
                $this->status = is_array($s) ? $s : false;
            } catch (AgentException) {
                $this->status = false;
            }
        }
        return $this->status === false ? null : $this->status;
    }

    public function writeEnabled(): bool
    {
        return (bool) ($this->get()['write_enabled'] ?? false);
    }
}
