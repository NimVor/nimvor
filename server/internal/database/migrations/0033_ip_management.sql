-- generated from panel/database/migrations/0033_ip_management.php — do not edit by hand
CREATE TABLE ip_addresses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id) ON DELETE CASCADE,
    address TEXT NOT NULL,
    family INTEGER NOT NULL,
    kind TEXT NOT NULL DEFAULT 'shared',
    reserved INTEGER NOT NULL DEFAULT 0,
    reason TEXT NOT NULL DEFAULT '',
    range_id INTEGER NULL,
    created_at TEXT NOT NULL,
    UNIQUE (server_id, address)
);

CREATE TABLE ipv6_ranges (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id) ON DELETE CASCADE,
    prefix TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    UNIQUE (server_id, prefix)
);

ALTER TABLE websites ADD COLUMN ipv4 TEXT NULL;

ALTER TABLE websites ADD COLUMN ipv6 TEXT NULL;

