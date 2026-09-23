<?php

declare(strict_types=1);

namespace WhaCP\Agent;

final class AgentHttpResponse
{
    public function __construct(
        public readonly int $status,
        public readonly string $body,
    ) {
    }
}
