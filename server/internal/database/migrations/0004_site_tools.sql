-- generated from panel/database/migrations/0004_site_tools.php — do not edit by hand
CREATE TABLE cron_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
    website_id INTEGER NOT NULL REFERENCES websites(id) ON DELETE CASCADE,
    schedule TEXT NOT NULL,
    command TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1,
    last_run_job TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE workers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
    website_id INTEGER NOT NULL REFERENCES websites(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    directory TEXT NOT NULL DEFAULT '',
    command TEXT NOT NULL,
    env TEXT NOT NULL DEFAULT '{}',
    restart TEXT NOT NULL DEFAULT 'always',
    memory_mb INTEGER NOT NULL DEFAULT 0,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (server_id, slug)
);

CREATE TABLE git_repositories (
    website_id INTEGER PRIMARY KEY REFERENCES websites(id) ON DELETE CASCADE,
    repo TEXT NOT NULL,
    branch TEXT NOT NULL DEFAULT 'main',
    path TEXT NOT NULL DEFAULT 'app',
    hooks TEXT NOT NULL DEFAULT '[]',
    auto_deploy INTEGER NOT NULL DEFAULT 0,
    webhook_token TEXT NOT NULL UNIQUE,
    webhook_secret_enc TEXT NOT NULL DEFAULT '',
    public_key TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE deployments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    website_id INTEGER NOT NULL REFERENCES websites(id) ON DELETE CASCADE,
    job_id TEXT NOT NULL DEFAULT '',
    trigger TEXT NOT NULL,
    branch TEXT NOT NULL DEFAULT '',
    commit_hash TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'running',
    error TEXT NOT NULL DEFAULT '',
    user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    finished_at TEXT NULL
);

CREATE INDEX deployments_site ON deployments (website_id, id);

CREATE TABLE backup_storages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL DEFAULT 's3',
    config_enc TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE backup_schedules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
    website_id INTEGER NULL REFERENCES websites(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('site', 'server')),
    frequency TEXT NOT NULL CHECK (frequency IN ('daily', 'weekly')),
    hour INTEGER NOT NULL DEFAULT 3,
    weekday INTEGER NOT NULL DEFAULT 0,
    include_files INTEGER NOT NULL DEFAULT 1,
    include_databases INTEGER NOT NULL DEFAULT 1,
    encrypt INTEGER NOT NULL DEFAULT 1,
    storage_id INTEGER NULL REFERENCES backup_storages(id) ON DELETE SET NULL,
    keep_local INTEGER NOT NULL DEFAULT 1,
    retention INTEGER NOT NULL DEFAULT 7,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_run_at TEXT NULL,
    last_status TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE TABLE backups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
    website_id INTEGER NULL REFERENCES websites(id) ON DELETE SET NULL,
    schedule_id INTEGER NULL REFERENCES backup_schedules(id) ON DELETE SET NULL,
    kind TEXT NOT NULL,
    domain TEXT NOT NULL DEFAULT '',
    backup_id TEXT NOT NULL DEFAULT '',
    label TEXT NOT NULL DEFAULT '',
    manifest TEXT NOT NULL DEFAULT '{}',
    size INTEGER NOT NULL DEFAULT 0,
    encrypted INTEGER NOT NULL DEFAULT 0,
    storage_id INTEGER NULL REFERENCES backup_storages(id) ON DELETE SET NULL,
    local INTEGER NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'running',
    error TEXT NOT NULL DEFAULT '',
    job_id TEXT NOT NULL DEFAULT '',
    user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    finished_at TEXT NULL
);

CREATE INDEX backups_site ON backups (server_id, website_id, id);

