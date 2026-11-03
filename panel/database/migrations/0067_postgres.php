<?php

declare(strict_types=1);

// PostgreSQL metadata. The server itself stays the source of truth for what exists; these tables record who owns
// what (a customer, a website), the privilege level NimVor granted, and the registered servers. The MySQL tables
// (databases, database_users, ...) are not touched.

return [
    <<<'SQL'
    CREATE TABLE pg_servers (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
        name TEXT NOT NULL,
        connection_type TEXT NOT NULL DEFAULT 'local',
        host TEXT NOT NULL DEFAULT '',
        port INTEGER NOT NULL DEFAULT 5432,
        username TEXT NOT NULL DEFAULT '',
        credential_enc TEXT NOT NULL DEFAULT '',
        ssl_mode TEXT NOT NULL DEFAULT '',
        ca_pem TEXT NOT NULL DEFAULT '',
        version TEXT NOT NULL DEFAULT '',
        status TEXT NOT NULL DEFAULT 'active',
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL
    )
    SQL,
    'CREATE INDEX idx_pg_servers_server ON pg_servers (server_id)',
    <<<'SQL'
    CREATE TABLE pg_roles (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        pg_server_id INTEGER NOT NULL REFERENCES pg_servers(id) ON DELETE CASCADE,
        owner_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
        website_id INTEGER NULL REFERENCES websites(id) ON DELETE SET NULL,
        name TEXT NOT NULL,
        password_enc TEXT NOT NULL DEFAULT '',
        status TEXT NOT NULL DEFAULT 'active',
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        UNIQUE (pg_server_id, name)
    )
    SQL,
    'CREATE INDEX idx_pg_roles_owner ON pg_roles (owner_id)',
    <<<'SQL'
    CREATE TABLE pg_databases (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        pg_server_id INTEGER NOT NULL REFERENCES pg_servers(id) ON DELETE CASCADE,
        owner_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
        website_id INTEGER NULL REFERENCES websites(id) ON DELETE SET NULL,
        owner_role_id INTEGER NULL REFERENCES pg_roles(id) ON DELETE SET NULL,
        name TEXT NOT NULL,
        encoding TEXT NOT NULL DEFAULT 'UTF8',
        status TEXT NOT NULL DEFAULT 'active',
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        UNIQUE (pg_server_id, name)
    )
    SQL,
    'CREATE INDEX idx_pg_databases_owner ON pg_databases (owner_id)',
    'CREATE INDEX idx_pg_databases_website ON pg_databases (website_id)',
    <<<'SQL'
    CREATE TABLE pg_grants (
        database_id INTEGER NOT NULL REFERENCES pg_databases(id) ON DELETE CASCADE,
        role_id INTEGER NOT NULL REFERENCES pg_roles(id) ON DELETE CASCADE,
        level TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        PRIMARY KEY (database_id, role_id)
    )
    SQL,
];
