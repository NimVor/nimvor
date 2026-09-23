<?php

declare(strict_types=1);

namespace WhaCP\Agent;

use WhaCP\Support\UserFacingException;

/**
 * The agent refused or failed an action. The message is safe to display.
 */
class AgentException extends UserFacingException
{
    public function __construct(
        string $message,
        string $reason = '',
        string $remedy = '',
        private readonly string $errorCode = 'agent_error',
        int $status = 502,
        ?\Throwable $previous = null,
    ) {
        parent::__construct($message, $reason, $remedy, $status, $previous);
    }

    public function errorCode(): string
    {
        return $this->errorCode;
    }
}
