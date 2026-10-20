-- generated from panel/database/migrations/0021_account_options.php — do not edit by hand
CREATE TABLE account_options (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    options TEXT NOT NULL DEFAULT '{}',
    updated_at TEXT NOT NULL
);

