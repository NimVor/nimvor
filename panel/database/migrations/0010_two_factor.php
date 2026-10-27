<?php

declare(strict_types=1);

// Phase 28: two-factor sign-in (TOTP) for panel users.

return [
    <<<'SQL'
    CREATE TABLE user_two_factor (
        user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
        secret TEXT NOT NULL,
        recovery_codes TEXT NOT NULL DEFAULT '[]',
        last_step INTEGER NOT NULL DEFAULT 0,
        enabled_at TEXT NOT NULL
    )
    SQL,
];
