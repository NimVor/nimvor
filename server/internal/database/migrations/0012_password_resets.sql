-- generated from panel/database/migrations/0012_password_resets.php — do not edit by hand
CREATE TABLE password_resets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NULL UNIQUE,
    ip TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at INTEGER NULL
);

CREATE INDEX password_resets_user ON password_resets (user_id, created_at);

CREATE INDEX password_resets_ip ON password_resets (ip, created_at);

