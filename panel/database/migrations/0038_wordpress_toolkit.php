<?php

declare(strict_types=1);

// WordPress toolkit additions: the cron job that took over wp-cron.php,
// vulnerability protection and the last vulnerability check (JSON list,
// public data only), and which installation a copy was cloned from.

return [
    'ALTER TABLE wordpress_installs ADD COLUMN cron_job_id INTEGER NULL',
    'ALTER TABLE wordpress_installs ADD COLUMN vuln_protection INTEGER NOT NULL DEFAULT 0',
    "ALTER TABLE wordpress_installs ADD COLUMN vulns TEXT NOT NULL DEFAULT ''",
    'ALTER TABLE wordpress_installs ADD COLUMN vulns_checked_at TEXT NULL',
    'ALTER TABLE wordpress_installs ADD COLUMN cloned_from INTEGER NULL',
    // New WordPress installations get hardened once (existing ones are left as they are).
    'ALTER TABLE app_installs ADD COLUMN hardened INTEGER NOT NULL DEFAULT 0',
    'UPDATE app_installs SET hardened = 1',
];
