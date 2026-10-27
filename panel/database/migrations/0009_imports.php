<?php

declare(strict_types=1);

// Phase 27: imports of existing websites.

return [
    <<<'SQL'
    CREATE TABLE website_imports (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        website_id INTEGER NULL REFERENCES websites(id) ON DELETE SET NULL,
        domain TEXT NOT NULL,
        source TEXT NOT NULL,
        database_name TEXT NOT NULL DEFAULT '',
        status TEXT NOT NULL DEFAULT 'running',
        error TEXT NOT NULL DEFAULT '',
        job_id TEXT NOT NULL DEFAULT '',
        user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
        created_at TEXT NOT NULL,
        finished_at TEXT NULL
    )
    SQL,
    'CREATE INDEX website_imports_site ON website_imports (website_id)',
];
