<?php

declare(strict_types=1);

// A customer's system account (Linux user): created with their first website
// and reused by the ones after it, also when every website was removed.

return [
    <<<'SQL'
    CREATE TABLE hosting_accounts (
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id) ON DELETE CASCADE,
        owner_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
        system_user TEXT NOT NULL,
        created_at TEXT NOT NULL DEFAULT '',
        PRIMARY KEY (server_id, system_user)
    )
    SQL,
    <<<'SQL'
    INSERT OR IGNORE INTO hosting_accounts (server_id, owner_id, system_user, created_at)
    SELECT server_id, owner_id, system_user, MIN(created_at) FROM websites WHERE owner_id IS NOT NULL AND parent_id IS NULL GROUP BY server_id, owner_id, system_user
    SQL,
];
