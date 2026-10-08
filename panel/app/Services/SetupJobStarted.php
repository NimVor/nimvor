<?php

declare(strict_types=1);

namespace WhaCP\Services;

/** @internal A wizard step started an agent job (see ServerSetupService::runStep). */
final class SetupJobStarted extends \RuntimeException
{
    public function __construct(public readonly string $job, string $message)
    {
        parent::__construct($message);
    }
}
