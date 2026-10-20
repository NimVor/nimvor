<?php

declare(strict_types=1);

// Remote MongoDB servers the panel can connect to (to copy a database onto
// this server). The password is stored encrypted (libsodium, via Crypto) and
// never returned to the browser.

return [
    <<<'SQL'
    CREATE TABLE mongo_remotes (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
        name TEXT NOT NULL,
        host TEXT NOT NULL,
        port INTEGER NOT NULL DEFAULT 27017,
        username TEXT NOT NULL DEFAULT '',
        password_enc TEXT NOT NULL DEFAULT '',
        auth_db TEXT NOT NULL DEFAULT 'admin',
        tls INTEGER NOT NULL DEFAULT 0,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL
    )
    SQL,
    "CREATE INDEX idx_mongo_remotes_server ON mongo_remotes (server_id)",
];
