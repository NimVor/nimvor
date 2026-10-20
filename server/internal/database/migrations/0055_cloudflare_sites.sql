-- generated from panel/database/migrations/0055_cloudflare_sites.php — do not edit by hand
CREATE TABLE cloudflare_sites (
    website_id INTEGER PRIMARY KEY REFERENCES websites(id) ON DELETE CASCADE,
    token_enc TEXT NOT NULL,
    zone_id TEXT NOT NULL DEFAULT '',
    zone_name TEXT NOT NULL DEFAULT '',
    verified_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

