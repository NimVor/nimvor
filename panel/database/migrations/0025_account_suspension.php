<?php

declare(strict_types=1);

// Account suspension: a suspended account (or a customer of a suspended
// reseller) cannot sign in, and its websites, mail logins, database users,
// FTP/SFTP, cron jobs and workers are turned off until it is resumed.
// Nothing is deleted.

return [
    'ALTER TABLE users ADD COLUMN suspended_at TEXT NULL',
    "ALTER TABLE users ADD COLUMN suspend_reason TEXT NOT NULL DEFAULT ''",
];
