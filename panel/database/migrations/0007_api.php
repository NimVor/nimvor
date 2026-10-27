<?php

declare(strict_types=1);

// Phase 22: personal API tokens (stored as SHA-256) and rate limiting.

return [
    <<<'SQL'
    CREATE TABLE api_tokens (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
        name TEXT NOT NULL,
        token_hash TEXT NOT NULL UNIQUE,
        prefix TEXT NOT NULL,
        ability TEXT NOT NULL CHECK (ability IN ('read', 'write')),
        expires_at TEXT NULL,
        last_used_at TEXT NULL,
        last_used_ip TEXT NULL,
        created_at TEXT NOT NULL
    )
    SQL,
    <<<'SQL'
    CREATE TABLE api_rate (
        token_id INTEGER NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
        window INTEGER NOT NULL,
        hits INTEGER NOT NULL,
        PRIMARY KEY (token_id, window)
    )
    SQL,
];
