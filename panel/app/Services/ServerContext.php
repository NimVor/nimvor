<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Http\Session\SessionInterface;

/**
 * The server the current user is working on (multi-server). Defaults to
 * the local server (id 1).
 */
final class ServerContext
{
    private const KEY = '_server_id';

    public function __construct(private readonly SessionInterface $session)
    {
    }

    /** Set by the CLI scheduler, which has no session. */
    private ?int $forced = null;

    public function force(?int $id): void
    {
        $this->forced = $id;
    }

    public function forced(): ?int
    {
        return $this->forced;
    }

    public function id(): int
    {
        if ($this->forced !== null) {
            return $this->forced;
        }
        $id = $this->session->get(self::KEY, 1);
        return is_int($id) && $id > 0 ? $id : 1;
    }

    public function switchTo(int $id): void
    {
        $this->session->set(self::KEY, $id);
    }
}
