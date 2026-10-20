-- generated from panel/database/migrations/0034_plan_resource_limits.php — do not edit by hand
ALTER TABLE plans ADD COLUMN cpu_percent INTEGER NOT NULL DEFAULT 0;

ALTER TABLE plans ADD COLUMN memory_mb INTEGER NOT NULL DEFAULT 0;

ALTER TABLE plans ADD COLUMN tasks_max INTEGER NOT NULL DEFAULT 0;

ALTER TABLE plans ADD COLUMN io_read_mbps INTEGER NOT NULL DEFAULT 0;

ALTER TABLE plans ADD COLUMN io_write_mbps INTEGER NOT NULL DEFAULT 0;

