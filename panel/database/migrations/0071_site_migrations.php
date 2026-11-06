<?php

declare(strict_types=1);

// Website migrations from other servers (Migrations). The panel keeps the wizard's choices and the last state the
// agent reported; the source login is kept only while a migration is unfinished, encrypted, and is erased when it
// completes or is cancelled.

return [
    <<<'SQL'
    CREATE TABLE site_migrations (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
        panel TEXT NOT NULL,
        status TEXT NOT NULL DEFAULT 'draft',
        source_host TEXT NOT NULL DEFAULT '',
        source_port INTEGER NOT NULL DEFAULT 22,
        source_user TEXT NOT NULL DEFAULT '',
        sealed TEXT NOT NULL DEFAULT '',
        pending_key TEXT NOT NULL DEFAULT '{}',
        info TEXT NOT NULL DEFAULT '{}',
        discovery TEXT NOT NULL DEFAULT '{}',
        selection TEXT NOT NULL DEFAULT '{}',
        plan TEXT NOT NULL DEFAULT '{}',
        assessment TEXT NOT NULL DEFAULT '{}',
        agent_id TEXT NOT NULL DEFAULT '',
        job_id TEXT NOT NULL DEFAULT '',
        error TEXT NOT NULL DEFAULT '',
        user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        finished_at TEXT NULL
    )
    SQL,
    'CREATE INDEX site_migrations_server ON site_migrations (server_id, id)',
];
