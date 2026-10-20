-- generated from panel/database/migrations/0010_two_factor.php — do not edit by hand
CREATE TABLE user_two_factor (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    secret TEXT NOT NULL,
    recovery_codes TEXT NOT NULL DEFAULT '[]',
    last_step INTEGER NOT NULL DEFAULT 0,
    enabled_at TEXT NOT NULL
);

