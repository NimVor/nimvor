-- generated from panel/database/migrations/0037_disk_quotas.php — do not edit by hand
CREATE TABLE disk_quotas (
    server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id) ON DELETE CASCADE,
    system_user TEXT NOT NULL,
    used_kb INTEGER NOT NULL DEFAULT 0,
    hard_kb INTEGER NOT NULL DEFAULT 0,
    measured_at TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (server_id, system_user)
);

