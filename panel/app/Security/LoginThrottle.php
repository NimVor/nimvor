<?php

declare(strict_types=1);

namespace NimVor\Security;

use NimVor\Repositories\LoginAttemptRepository;

/**
 * Brute-force protection: limits failed logins per IP and per username
 * within a sliding window.
 */
final class LoginThrottle
{
    /** @var callable(): int */
    private $clock;

    public function __construct(
        private readonly LoginAttemptRepository $attempts,
        private readonly int $maxPerIp = 10,
        private readonly int $maxPerUser = 5,
        private readonly int $windowSeconds = 900,
        ?callable $clock = null,
    ) {
        $this->clock = $clock ?? time(...);
    }

    /** Seconds until another attempt is allowed (0 = allowed now). */
    public function retryAfter(string $username, string $ip): int
    {
        $now = ($this->clock)();
        $since = $now - $this->windowSeconds;
        $blocked = $this->attempts->failuresForIp($ip, $since) >= $this->maxPerIp
            || ($username !== '' && $this->attempts->failuresForUser($username, $since) >= $this->maxPerUser);
        if (!$blocked) {
            return 0;
        }
        $oldest = $this->attempts->oldestFailureForIp($ip, $since) ?? $now;
        return max(60, $oldest + $this->windowSeconds - $now);
    }

    public function recordFailure(string $username, string $ip): void
    {
        $now = ($this->clock)();
        $this->attempts->record($username, $ip, false, $now);
        $this->attempts->prune($now - 86400);
    }

    public function recordSuccess(string $username, string $ip): void
    {
        $this->attempts->record($username, $ip, true, ($this->clock)());
        $this->attempts->clearFailures($username, $ip);
    }
}
