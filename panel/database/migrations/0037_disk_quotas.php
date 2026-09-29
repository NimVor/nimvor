<?php

declare(strict_types=1);

// Disk quotas: each website system user's use and limit (KiB) on a server's
// websites file system, as last read from and set by the agent.

return [
    <<<'SQL'
    CREATE TABLE disk_quotas (
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id) ON DELETE CASCADE,
        system_user TEXT NOT NULL,
        used_kb INTEGER NOT NULL DEFAULT 0,
        hard_kb INTEGER NOT NULL DEFAULT 0,
        measured_at TEXT NOT NULL DEFAULT '',
        PRIMARY KEY (server_id, system_user)
    )
    SQL,
];
