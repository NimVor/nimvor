<?php

declare(strict_types=1);

namespace NimVor\Support;

use RuntimeException;

/**
 * Carries field-level, user-facing validation errors.
 */
final class ValidationException extends RuntimeException
{
    /** @param array<string, string> $errors */
    public function __construct(private readonly array $errors, string $message = 'Please correct the highlighted fields.')
    {
        parent::__construct($message);
    }

    /** @return array<string, string> */
    public function errors(): array
    {
        return $this->errors;
    }
}
