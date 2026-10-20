-- generated from panel/database/migrations/0046_backup_all_sites.php — do not edit by hand
ALTER TABLE backup_schedules ADD COLUMN all_sites INTEGER NOT NULL DEFAULT 0;

