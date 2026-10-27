<?php

declare(strict_types=1);

// Password reset by email.

return [
    <<<'SQL'
    CREATE TABLE password_resets (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id INTEGER NULL REFERENCES users(id) ON DELETE CASCADE,
        token_hash TEXT NULL UNIQUE,
        ip TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        expires_at INTEGER NOT NULL,
        used_at INTEGER NULL
    )
    SQL,
    'CREATE INDEX password_resets_user ON password_resets (user_id, created_at)',
    'CREATE INDEX password_resets_ip ON password_resets (ip, created_at)',
];
