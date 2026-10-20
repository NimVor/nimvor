-- generated from panel/database/migrations/0022_auto_ssl.php — do not edit by hand
ALTER TABLE websites ADD COLUMN auto_ssl TEXT NOT NULL DEFAULT '';

ALTER TABLE websites ADD COLUMN auto_ssl_tries INTEGER NOT NULL DEFAULT 0;

ALTER TABLE websites ADD COLUMN auto_ssl_next TEXT NULL;

ALTER TABLE websites ADD COLUMN auto_ssl_note TEXT NOT NULL DEFAULT '';

