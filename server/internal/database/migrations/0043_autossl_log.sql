-- generated from panel/database/migrations/0043_autossl_log.php — do not edit by hand
CREATE TABLE autossl_log (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL,
        website_id INTEGER NULL,
        domain TEXT NOT NULL,
        result TEXT NOT NULL,
        message TEXT NOT NULL DEFAULT '',
        created_at TEXT NOT NULL
    );

CREATE INDEX autossl_log_server ON autossl_log (server_id, id);

