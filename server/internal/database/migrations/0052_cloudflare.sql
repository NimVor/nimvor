-- generated from panel/database/migrations/0052_cloudflare.php — do not edit by hand
CREATE TABLE cloudflare_accounts (
    server_id INTEGER NOT NULL PRIMARY KEY DEFAULT 1 REFERENCES servers(id),
    token_enc TEXT NOT NULL,
    account_email TEXT NOT NULL DEFAULT '',
    verified_at TEXT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

