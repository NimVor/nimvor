-- generated from panel/database/migrations/0045_backup_storage_active.php — do not edit by hand
ALTER TABLE backup_storages ADD COLUMN active INTEGER NOT NULL DEFAULT 1;

ALTER TABLE backup_storages ADD COLUMN test_ok INTEGER NOT NULL DEFAULT 1;

ALTER TABLE backup_storages ADD COLUMN test_error TEXT NOT NULL DEFAULT '';

ALTER TABLE backup_storages ADD COLUMN tested_at TEXT NULL;

UPDATE backup_storages SET tested_at = created_at;

CREATE TABLE account_restores (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
    path TEXT NOT NULL,
    username TEXT NOT NULL,
    manifest TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'importing',
    job_id TEXT NOT NULL DEFAULT '',
    report TEXT NOT NULL DEFAULT '[]',
    user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

