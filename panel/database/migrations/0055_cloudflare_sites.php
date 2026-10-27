<?php

declare(strict_types=1);

// Per-website Cloudflare: a customer connects their OWN Cloudflare API token
// (scoped to their zone), stored encrypted, and WhaCP records which zone of
// that token matches the website's domain. Separate from the server-wide
// cloudflare_accounts table.

return [
    <<<'SQL'
    CREATE TABLE cloudflare_sites (
        website_id INTEGER PRIMARY KEY REFERENCES websites(id) ON DELETE CASCADE,
        token_enc TEXT NOT NULL,
        zone_id TEXT NOT NULL DEFAULT '',
        zone_name TEXT NOT NULL DEFAULT '',
        verified_at TEXT NOT NULL,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL
    )
    SQL,
];
