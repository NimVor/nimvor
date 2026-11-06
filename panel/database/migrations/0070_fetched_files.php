<?php

declare(strict_types=1);

// Files fetched from a remote SFTP or FTPS server (Backups > Remote fetched files). The panel keeps only where a
// file came from and what the agent reported; the login used for the transfer is never stored.

return [
    <<<'SQL'
    CREATE TABLE fetched_files (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
        protocol TEXT NOT NULL,
        host TEXT NOT NULL,
        port INTEGER NOT NULL,
        username TEXT NOT NULL,
        remote_path TEXT NOT NULL,
        file_name TEXT NOT NULL DEFAULT '',
        size INTEGER NOT NULL DEFAULT 0,
        sha256 TEXT NOT NULL DEFAULT '',
        status TEXT NOT NULL DEFAULT 'running',
        error TEXT NOT NULL DEFAULT '',
        job_id TEXT NOT NULL DEFAULT '',
        user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
        created_at TEXT NOT NULL,
        finished_at TEXT NULL
    )
    SQL,
    'CREATE INDEX fetched_files_server ON fetched_files (server_id, id)',
];
