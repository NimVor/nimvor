<?php

declare(strict_types=1);

// Security keys and passkeys (WebAuthn) as a second sign-in factor.

return [
    <<<'SQL'
    CREATE TABLE webauthn_credentials (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
        credential_id TEXT NOT NULL UNIQUE,
        public_key TEXT NOT NULL,
        sign_count INTEGER NOT NULL DEFAULT 0,
        name TEXT NOT NULL,
        created_at TEXT NOT NULL,
        last_used_at TEXT NULL
    )
    SQL,
    'CREATE INDEX webauthn_user ON webauthn_credentials (user_id)',
];
