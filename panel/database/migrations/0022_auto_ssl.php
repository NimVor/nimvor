<?php

declare(strict_types=1);

// Automatic SSL: a new website waits (auto_ssl = pending) until its domain
// points to this server, then gets a Let's Encrypt certificate. The note
// says what happened last.

return [
    "ALTER TABLE websites ADD COLUMN auto_ssl TEXT NOT NULL DEFAULT ''",
    'ALTER TABLE websites ADD COLUMN auto_ssl_tries INTEGER NOT NULL DEFAULT 0',
    'ALTER TABLE websites ADD COLUMN auto_ssl_next TEXT NULL',
    "ALTER TABLE websites ADD COLUMN auto_ssl_note TEXT NOT NULL DEFAULT ''",
];
