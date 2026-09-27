<?php

declare(strict_types=1);

// Feature Manager: named feature lists assigned to plans. A list stores the
// features it turns OFF, so features added in later releases are on for
// existing lists. A plan without a list has every feature.

return [
    <<<'SQL'
    CREATE TABLE feature_lists (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        name TEXT NOT NULL UNIQUE,
        description TEXT NOT NULL DEFAULT '',
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL
    )
    SQL,
    <<<'SQL'
    CREATE TABLE feature_list_disabled (
        list_id INTEGER NOT NULL REFERENCES feature_lists(id) ON DELETE CASCADE,
        feature TEXT NOT NULL,
        PRIMARY KEY (list_id, feature)
    )
    SQL,
    'ALTER TABLE plans ADD COLUMN feature_list_id INTEGER NULL REFERENCES feature_lists(id) ON DELETE SET NULL',
];
