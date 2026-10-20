-- generated from panel/database/migrations/0008_apps.php — do not edit by hand
CREATE TABLE app_installs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    website_id INTEGER NOT NULL REFERENCES websites(id) ON DELETE CASCADE,
    app TEXT NOT NULL,
    path TEXT NOT NULL,
    database_id INTEGER NULL REFERENCES databases(id) ON DELETE SET NULL,
    admin_user TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'running',
    error TEXT NOT NULL DEFAULT '',
    job_id TEXT NOT NULL DEFAULT '',
    user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    finished_at TEXT NULL
);

