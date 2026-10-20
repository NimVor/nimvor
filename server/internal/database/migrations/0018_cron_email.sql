-- generated from panel/database/migrations/0018_cron_email.php — do not edit by hand
ALTER TABLE cron_jobs ADD COLUMN email TEXT NOT NULL DEFAULT '';

