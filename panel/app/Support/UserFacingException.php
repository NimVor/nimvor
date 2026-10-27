<?php

declare(strict_types=1);

namespace WhaCP\Support;

use RuntimeException;
use Throwable;

/**
 * An error that is safe to show to users, structured as
 * "what failed / why / what to do".
 */
class UserFacingException extends RuntimeException
{
    public function __construct(
        string $message,
        private readonly string $reason = '',
        private readonly string $remedy = '',
        private readonly int $status = 400,
        ?Throwable $previous = null,
    ) {
        parent::__construct($message, 0, $previous);
    }

    public function reason(): string
    {
        return $this->reason;
    }

    public function remedy(): string
    {
        return $this->remedy;
    }

    public function status(): int
    {
        return $this->status;
    }

    /** @return array{message: string, reason: string, action: string} */
    public function toArray(): array
    {
        return ['message' => $this->getMessage(), 'reason' => $this->reason, 'action' => $this->remedy];
    }
}
