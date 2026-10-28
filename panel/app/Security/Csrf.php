<?php

declare(strict_types=1);

namespace NimVor\Security;

use NimVor\Http\Session\SessionInterface;

/**
 * Synchronizer-token CSRF protection (one token per session).
 */
final class Csrf
{
    private const KEY = '_csrf_token';

    public function __construct(private readonly SessionInterface $session)
    {
    }

    public function token(): string
    {
        $token = $this->session->get(self::KEY);
        if (!is_string($token) || strlen($token) !== 64) {
            $token = bin2hex(random_bytes(32));
            $this->session->set(self::KEY, $token);
        }
        return $token;
    }

    public function validate(?string $submitted): bool
    {
        $expected = $this->session->get(self::KEY);
        return is_string($expected) && is_string($submitted) && $submitted !== '' && hash_equals($expected, $submitted);
    }

    public function rotate(): void
    {
        $this->session->remove(self::KEY);
    }
}
