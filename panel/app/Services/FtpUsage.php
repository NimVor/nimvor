<?php

declare(strict_types=1);

namespace NimVor\Services;

use NimVor\Agent\AgentClient;
use NimVor\Database\Connection;

/** FTP accounts of users' websites (the accounts live on the server). */
final class FtpUsage
{
    public function __construct(
        private readonly Connection $db,
        private readonly AgentClient $agent,
        private readonly ServerContext $server,
    ) {
    }

    /** @param list<int> $owners */
    public function count(array $owners): int
    {
        if ($owners === []) {
            return 0;
        }
        $users = array_column($this->db->fetchAll('SELECT DISTINCT system_user FROM websites WHERE server_id = ? AND owner_id IN (' . implode(',', array_map('intval', $owners)) . ')', [$this->server->id()]), 'system_user');
        if ($users === []) {
            return 0;
        }
        $n = 0;
        foreach ((array) (((array) $this->agent->call('ftp.status'))['accounts'] ?? []) as $a) {
            $n += in_array((string) (((array) $a)['user'] ?? ''), $users, true) ? 1 : 0;
        }
        return $n;
    }
}
