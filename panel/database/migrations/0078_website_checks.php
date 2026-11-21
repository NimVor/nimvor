<?php

declare(strict_types=1);

// Dashboard 2.0: one row per real HTTP check of a website (status code and time
// to first byte, or why nothing answered). Kept for two weeks.

return [
    <<<'SQL'
    CREATE TABLE website_checks (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL,
        website_id INTEGER NOT NULL,
        domain TEXT NOT NULL,
        checked_at INTEGER NOT NULL,
        status INTEGER NOT NULL DEFAULT 0,
        ms INTEGER,
        error TEXT NOT NULL DEFAULT ''
    )
    SQL,
    'CREATE INDEX website_checks_site ON website_checks (server_id, website_id, checked_at)',
];
