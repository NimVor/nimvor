-- generated from panel/database/migrations/0036_bandwidth_block.php — do not edit by hand
ALTER TABLE users ADD COLUMN bw_blocked_month TEXT NOT NULL DEFAULT '';

ALTER TABLE websites ADD COLUMN bandwidth_applied INTEGER NOT NULL DEFAULT 0;

