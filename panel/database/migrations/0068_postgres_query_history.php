<?php

declare(strict_types=1);

// What was run in the PostgreSQL SQL editor, by whom and how it went. The statement itself is not kept, only a
// "fingerprint" (literals replaced by ?), because a statement can contain passwords and personal data.

return [
    <<<'SQL'
    CREATE TABLE pg_query_history (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        pg_database_id INTEGER NOT NULL REFERENCES pg_databases(id) ON DELETE CASCADE,
        user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
        fingerprint TEXT NOT NULL,
        mode TEXT NOT NULL DEFAULT 'read',
        duration_ms INTEGER NOT NULL DEFAULT 0,
        rows_returned INTEGER NOT NULL DEFAULT 0,
        status TEXT NOT NULL DEFAULT 'ok',
        created_at TEXT NOT NULL
    )
    SQL,
    'CREATE INDEX idx_pg_query_history_lookup ON pg_query_history (pg_database_id, user_id, id)',
];
