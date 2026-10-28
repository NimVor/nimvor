<?php

declare(strict_types=1);

namespace NimVor\Agent;

final class AgentHttpResponse
{
    public function __construct(
        public readonly int $status,
        public readonly string $body,
    ) {
    }
}
