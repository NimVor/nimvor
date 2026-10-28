<?php

declare(strict_types=1);

// Phase 7: MySQL/MariaDB databases and users managed by NimVor.

return [
    <<<'SQL'
    CREATE TABLE databases (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
        website_id INTEGER NULL REFERENCES websites(id) ON DELETE SET NULL,
        owner_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
        name TEXT NOT NULL,
        created_at TEXT NOT NULL,
        UNIQUE (server_id, name)
    )
    SQL,
    <<<'SQL'
    CREATE TABLE database_users (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
        owner_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
        username TEXT NOT NULL,
        host TEXT NOT NULL DEFAULT 'localhost',
        password_enc TEXT NOT NULL DEFAULT '',
        created_at TEXT NOT NULL,
        UNIQUE (server_id, username, host)
    )
    SQL,
    <<<'SQL'
    CREATE TABLE database_grants (
        database_id INTEGER NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
        user_id INTEGER NOT NULL REFERENCES database_users(id) ON DELETE CASCADE,
        privileges TEXT NOT NULL,
        PRIMARY KEY (database_id, user_id)
    )
    SQL,
];
