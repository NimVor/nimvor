<?php

declare(strict_types=1);

// Cloudflare integration: one API token per server (encrypted), used to
// manage the free-plan CDN/DNS of zones on this Cloudflare account.

return [
    <<<'SQL'
    CREATE TABLE cloudflare_accounts (
        server_id INTEGER NOT NULL PRIMARY KEY DEFAULT 1 REFERENCES servers(id),
        token_enc TEXT NOT NULL,
        account_email TEXT NOT NULL DEFAULT '',
        verified_at TEXT NULL,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL
    )
    SQL,
];
