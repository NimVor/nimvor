<?php

declare(strict_types=1);

// WordPress toolkit: installations found in websites (by the scan), with
// what the last scan and overview reported. Nothing here is a secret: the
// agent never returns database passwords.

return [
    <<<'SQL'
    CREATE TABLE wordpress_installs (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id) ON DELETE CASCADE,
        website_id INTEGER NOT NULL REFERENCES websites(id) ON DELETE CASCADE,
        path TEXT NOT NULL DEFAULT '',
        version TEXT NOT NULL DEFAULT '',
        db_name TEXT NOT NULL DEFAULT '',
        db_host TEXT NOT NULL DEFAULT '',
        table_prefix TEXT NOT NULL DEFAULT '',
        debug INTEGER NOT NULL DEFAULT 0,
        file_edit_disabled INTEGER NOT NULL DEFAULT 0,
        config_mode TEXT NOT NULL DEFAULT '',
        maintenance INTEGER NOT NULL DEFAULT 0,
        siteurl TEXT NOT NULL DEFAULT '',
        latest TEXT NOT NULL DEFAULT '',
        plugin_updates INTEGER NOT NULL DEFAULT 0,
        theme_updates INTEGER NOT NULL DEFAULT 0,
        issues INTEGER NOT NULL DEFAULT 0,
        scanned_at TEXT NOT NULL,
        checked_at TEXT NULL,
        UNIQUE (website_id, path)
    )
    SQL,
    'CREATE INDEX wordpress_installs_server ON wordpress_installs (server_id)',
];
