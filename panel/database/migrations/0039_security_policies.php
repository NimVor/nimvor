<?php

declare(strict_types=1);

// Security policies: the IP addresses each user has verified for signing
// in (Limit logins to verified IP addresses).

return [
    'CREATE TABLE login_ips (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
        ip TEXT NOT NULL,
        verified_at TEXT NOT NULL,
        last_used_at TEXT NULL,
        UNIQUE (user_id, ip)
    )',
];
