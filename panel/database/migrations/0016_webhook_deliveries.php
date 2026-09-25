<?php

declare(strict_types=1);

// Deployment webhooks already processed, by a hash of their signed body:
// a captured delivery cannot be replayed to deploy again (F-05).

return [
    <<<'SQL'
    CREATE TABLE webhook_deliveries (
        website_id INTEGER NOT NULL REFERENCES websites(id) ON DELETE CASCADE,
        body_sha256 TEXT NOT NULL,
        received_at TEXT NOT NULL,
        PRIMARY KEY (website_id, body_sha256)
    )
    SQL,
];
