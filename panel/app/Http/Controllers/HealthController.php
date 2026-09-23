<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Database\Connection;
use WhaCP\Http\Request;
use WhaCP\Http\Response;

/**
 * Unauthenticated liveness probe used by the installer and upgrade health
 * checks. It reveals nothing beyond "up" or "down".
 */
final class HealthController
{
    public function __construct(private readonly Connection $db)
    {
    }

    public function show(Request $request): Response
    {
        try {
            $this->db->scalar('SELECT 1');
            return Response::json(['status' => 'ok']);
        } catch (\Throwable) {
            return Response::json(['status' => 'error'], 503);
        }
    }
}
