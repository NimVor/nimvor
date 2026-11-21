<?php

declare(strict_types=1);

// Dashboard 2.0: each user's widget layout (which widgets, their order and size),
// and a short-lived cache of what the widgets measured so a refresh never
// repeats an expensive measurement.

return [
    <<<'SQL'
    CREATE TABLE dashboard_layouts (
        user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
        version INTEGER NOT NULL DEFAULT 1,
        layout TEXT NOT NULL DEFAULT '{}',
        updated_at TEXT NOT NULL
    )
    SQL,
    <<<'SQL'
    CREATE TABLE widget_cache (
        widget TEXT NOT NULL,
        scope TEXT NOT NULL DEFAULT 'server',
        payload TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        PRIMARY KEY (widget, scope)
    )
    SQL,
];
