-- generated from panel/database/migrations/0017_notifications.php — do not edit by hand
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
);

CREATE INDEX notifications_created ON notifications (created_at);

CREATE INDEX notifications_website ON notifications (website_id);

CREATE TABLE notification_reads (
    notification_id INTEGER NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at TEXT NOT NULL,
    PRIMARY KEY (notification_id, user_id)
);

ALTER TABLE users ADD COLUMN notify_email INTEGER NOT NULL DEFAULT 0;

ALTER TABLE agent_jobs ADD COLUMN settled_at TEXT NULL;

