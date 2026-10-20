-- generated from panel/database/migrations/0040_brute_force_protection.php — do not edit by hand
CREATE TABLE hulk_events (
        id TEXT PRIMARY KEY,
        server_id INTEGER NOT NULL DEFAULT 1,
        time INTEGER NOT NULL,
        service TEXT NOT NULL,
        authservice TEXT NOT NULL,
        user TEXT NOT NULL DEFAULT '',
        ip TEXT NOT NULL,
        country TEXT NOT NULL DEFAULT '',
        ok INTEGER NOT NULL DEFAULT 0
    );

CREATE INDEX hulk_events_ip ON hulk_events (ip, time);

CREATE INDEX hulk_events_user ON hulk_events (user, time);

CREATE INDEX hulk_events_time ON hulk_events (time);

CREATE TABLE hulk_blocks (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        kind TEXT NOT NULL,
        ip TEXT NULL,
        user TEXT NULL,
        reason TEXT NOT NULL,
        service TEXT NOT NULL DEFAULT '',
        authservice TEXT NOT NULL DEFAULT '',
        failures INTEGER NOT NULL DEFAULT 0,
        firewall INTEGER NOT NULL DEFAULT 0,
        created_at INTEGER NOT NULL,
        expires_at INTEGER NOT NULL
    );

CREATE INDEX hulk_blocks_ip ON hulk_blocks (ip, expires_at);

CREATE INDEX hulk_blocks_user ON hulk_blocks (user, expires_at);

CREATE TABLE hulk_countries (
        code TEXT PRIMARY KEY,
        status TEXT NOT NULL
    );

CREATE TABLE geo_ranges (
        start BLOB NOT NULL,
        end BLOB NOT NULL,
        country TEXT NOT NULL
    );

CREATE INDEX geo_ranges_start ON geo_ranges (start);

