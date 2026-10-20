-- generated from panel/database/migrations/0039_security_policies.php — do not edit by hand
CREATE TABLE login_ips (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
        ip TEXT NOT NULL,
        verified_at TEXT NOT NULL,
        last_used_at TEXT NULL,
        UNIQUE (user_id, ip)
    );

