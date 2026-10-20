-- generated from panel/database/migrations/0016_webhook_deliveries.php — do not edit by hand
CREATE TABLE webhook_deliveries (
    website_id INTEGER NOT NULL REFERENCES websites(id) ON DELETE CASCADE,
    body_sha256 TEXT NOT NULL,
    received_at TEXT NOT NULL,
    PRIMARY KEY (website_id, body_sha256)
);

