-- generated from panel/database/migrations/0032_dns_record_disabled.php — do not edit by hand
ALTER TABLE dns_records ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0;

