<?php

declare(strict_types=1);

// More hosting-plan resources (0 = unlimited), and monthly bandwidth per
// website: bytes sent this month and where the access log was last read.

return [
    'ALTER TABLE plans ADD COLUMN bandwidth_mb INTEGER NOT NULL DEFAULT 0',
    'ALTER TABLE plans ADD COLUMN max_ftp_accounts INTEGER NOT NULL DEFAULT 0',
    'ALTER TABLE plans ADD COLUMN max_email_accounts INTEGER NOT NULL DEFAULT 0',
    'ALTER TABLE plans ADD COLUMN max_email_quota_mb INTEGER NOT NULL DEFAULT 0',
    'ALTER TABLE plans ADD COLUMN max_subdomains INTEGER NOT NULL DEFAULT 0',
    'ALTER TABLE plans ADD COLUMN max_parked_domains INTEGER NOT NULL DEFAULT 0',
    'ALTER TABLE plans ADD COLUMN max_addon_domains INTEGER NOT NULL DEFAULT 0',
    "ALTER TABLE website_usage ADD COLUMN bw_month TEXT NOT NULL DEFAULT ''",
    'ALTER TABLE website_usage ADD COLUMN bw_bytes INTEGER NOT NULL DEFAULT 0',
    'ALTER TABLE website_usage ADD COLUMN bw_inode INTEGER NOT NULL DEFAULT 0',
    'ALTER TABLE website_usage ADD COLUMN bw_offset INTEGER NOT NULL DEFAULT 0',
];
