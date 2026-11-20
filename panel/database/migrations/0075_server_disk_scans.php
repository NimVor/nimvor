<?php

declare(strict_types=1);

// Server → Disk usage: the latest scans of the whole server's disk (a
// background job in the agent) and their result (JSON: places, websites,
// largest files).

return [
    <<<'SQL'
    CREATE TABLE server_disk_scans (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id) ON DELETE CASCADE,
        job_id TEXT NOT NULL DEFAULT '',
        status TEXT NOT NULL DEFAULT 'running',
        min_mb INTEGER NOT NULL DEFAULT 50,
        result TEXT NOT NULL DEFAULT '{}',
        error TEXT NOT NULL DEFAULT '',
        started_at TEXT NOT NULL,
        finished_at TEXT NULL
    )
    SQL,
    'CREATE INDEX server_disk_scans_server ON server_disk_scans (server_id, id)',
];
