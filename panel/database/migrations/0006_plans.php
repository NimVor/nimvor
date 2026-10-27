<?php

declare(strict_types=1);

// Phase 21: hosting plans. owner_id NULL = created by an administrator;
// otherwise the reseller who owns the plan and may assign it.

return [
    <<<'SQL'
    CREATE TABLE plans (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        owner_id INTEGER NULL REFERENCES users(id) ON DELETE CASCADE,
        name TEXT NOT NULL,
        description TEXT NOT NULL DEFAULT '',
        max_websites INTEGER NOT NULL DEFAULT 0,
        max_databases INTEGER NOT NULL DEFAULT 0,
        max_cron_jobs INTEGER NOT NULL DEFAULT 0,
        max_workers INTEGER NOT NULL DEFAULT 0,
        max_customers INTEGER NOT NULL DEFAULT 0,
        disk_quota_mb INTEGER NOT NULL DEFAULT 0,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL
    )
    SQL,
    'CREATE UNIQUE INDEX plans_owner_name ON plans (COALESCE(owner_id, 0), name)',
    'ALTER TABLE users ADD COLUMN plan_id INTEGER NULL REFERENCES plans(id) ON DELETE SET NULL',
    <<<'SQL'
    CREATE TABLE website_usage (
        website_id INTEGER PRIMARY KEY REFERENCES websites(id) ON DELETE CASCADE,
        bytes INTEGER NOT NULL,
        files INTEGER NOT NULL,
        measured_at TEXT NOT NULL
    )
    SQL,
];
