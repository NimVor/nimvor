<?php

declare(strict_types=1);

// Notifications shown in the panel (and optionally emailed): results of
// background jobs and monitoring alerts. Who may see one is decided when it
// is read, from its website (or owner) and the reader's scope, so a website
// that changes hands takes its history with it.

return [
    <<<'SQL'
    CREATE TABLE notifications (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id) ON DELETE CASCADE,
        website_id INTEGER NULL REFERENCES websites(id) ON DELETE CASCADE,
        owner_id INTEGER NULL REFERENCES users(id) ON DELETE CASCADE,
        kind TEXT NOT NULL,
        severity TEXT NOT NULL,
        title TEXT NOT NULL,
        body TEXT NOT NULL DEFAULT '',
        link TEXT NOT NULL DEFAULT '',
        job_id TEXT NOT NULL DEFAULT '',
        dedupe_key TEXT NOT NULL UNIQUE,
        created_at TEXT NOT NULL
    )
    SQL,
    'CREATE INDEX notifications_created ON notifications (created_at)',
    'CREATE INDEX notifications_website ON notifications (website_id)',
    <<<'SQL'
    CREATE TABLE notification_reads (
        notification_id INTEGER NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
        user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
        read_at TEXT NOT NULL,
        PRIMARY KEY (notification_id, user_id)
    )
    SQL,
    // Email me about failures (opt-in, per user).
    'ALTER TABLE users ADD COLUMN notify_email INTEGER NOT NULL DEFAULT 0',
    // When the result of a background job was recorded.
    'ALTER TABLE agent_jobs ADD COLUMN settled_at TEXT NULL',
];
