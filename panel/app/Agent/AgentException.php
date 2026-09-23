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
        private readonly string $details = '',
    ) {
        parent::__construct($message, $reason, $remedy, $status, $previous);
    }

    /** Redacted technical output (e.g. from nginx -t) for operators. */
    public function details(): string
    {
        return $this->details;
    }

    /** @return array{message: string, reason: string, action: string, details: string} */
    public function toArray(): array
    {
        return parent::toArray() + ['details' => $this->details];
    }

    public function errorCode(): string
    {
        return $this->errorCode;
    }
}
