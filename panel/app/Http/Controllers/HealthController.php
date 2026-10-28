<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Database\Connection;
use NimVor\Http\Request;
use NimVor\Http\Response;

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
