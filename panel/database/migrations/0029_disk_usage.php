<?php

declare(strict_types=1);

// Disk usage by folder of websites: the latest scan of each folder (a
// background job in the agent) and its result (JSON: name, kind, bytes,
// files per folder).

return [
    <<<'SQL'
    CREATE TABLE disk_usage_scans (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id) ON DELETE CASCADE,
        website_id INTEGER NOT NULL REFERENCES websites(id) ON DELETE CASCADE,
        path TEXT NOT NULL DEFAULT '',
        job_id TEXT NOT NULL DEFAULT '',
        status TEXT NOT NULL DEFAULT 'running',
        result TEXT NOT NULL DEFAULT '{}',
        error TEXT NOT NULL DEFAULT '',
        started_at TEXT NOT NULL,
        finished_at TEXT NULL
    )
    SQL,
    'CREATE INDEX disk_usage_site ON disk_usage_scans (website_id, path, id)',
];
