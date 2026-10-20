-- generated from panel/database/migrations/0038_wordpress_toolkit.php — do not edit by hand
ALTER TABLE wordpress_installs ADD COLUMN cron_job_id INTEGER NULL;

ALTER TABLE wordpress_installs ADD COLUMN vuln_protection INTEGER NOT NULL DEFAULT 0;

ALTER TABLE wordpress_installs ADD COLUMN vulns TEXT NOT NULL DEFAULT '';

ALTER TABLE wordpress_installs ADD COLUMN vulns_checked_at TEXT NULL;

ALTER TABLE wordpress_installs ADD COLUMN cloned_from INTEGER NULL;

ALTER TABLE app_installs ADD COLUMN hardened INTEGER NOT NULL DEFAULT 0;

UPDATE app_installs SET hardened = 1;

