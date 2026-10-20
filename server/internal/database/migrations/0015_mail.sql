-- generated from panel/database/migrations/0015_mail.php — do not edit by hand
CREATE TABLE mail_domains (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    domain TEXT NOT NULL,
    website_id INTEGER NULL REFERENCES websites(id) ON DELETE SET NULL,
    owner_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
    dkim_selector TEXT NOT NULL DEFAULT '',
    dkim_txt TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    UNIQUE (server_id, domain)
);

CREATE TABLE mail_mailboxes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    domain_id INTEGER NOT NULL REFERENCES mail_domains(id) ON DELETE CASCADE,
    local TEXT NOT NULL,
    quota_mb INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    UNIQUE (domain_id, local)
);

CREATE TABLE mail_aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    domain_id INTEGER NOT NULL REFERENCES mail_domains(id) ON DELETE CASCADE,
    local TEXT NOT NULL,
    destinations TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (domain_id, local)
);

CREATE INDEX mail_domains_owner ON mail_domains (owner_id);

