-- generated from panel/database/migrations/0025_account_suspension.php — do not edit by hand
ALTER TABLE users ADD COLUMN suspended_at TEXT NULL;

ALTER TABLE users ADD COLUMN suspend_reason TEXT NOT NULL DEFAULT '';

