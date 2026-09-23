<?php

declare(strict_types=1);

namespace WhaCP\Services;

use WhaCP\Http\Session\SessionInterface;

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

    public function id(): int
    {
        $id = $this->session->get(self::KEY, 1);
        return is_int($id) && $id > 0 ? $id : 1;
    }

    public function switchTo(int $id): void
    {
        $this->session->set(self::KEY, $id);
    }
}
