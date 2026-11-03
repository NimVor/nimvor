<?php

declare(strict_types=1);

// One automatic backup schedule per PostgreSQL database: daily or weekly (Sunday) at an hour (UTC), keeping the newest N.

return [
    <<<'SQL'
    CREATE TABLE pg_backup_schedules (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        pg_database_id INTEGER NOT NULL UNIQUE REFERENCES pg_databases(id) ON DELETE CASCADE,
        frequency TEXT NOT NULL DEFAULT 'daily',
        hour INTEGER NOT NULL DEFAULT 3,
        keep INTEGER NOT NULL DEFAULT 7,
        last_run_at TEXT NULL,
        last_status TEXT NULL,
        created_at TEXT NOT NULL
    )
    SQL,
];
