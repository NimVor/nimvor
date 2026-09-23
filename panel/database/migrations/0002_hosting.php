<?php

declare(strict_types=1);

// Phase 5+: servers, websites, domains and agent job tracking.
// Every hosting table carries server_id so multi-server works unchanged.

return [
    <<<'SQL'
    CREATE TABLE servers (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        name TEXT NOT NULL UNIQUE,
        kind TEXT NOT NULL CHECK (kind IN ('local', 'remote')),
        url TEXT NOT NULL DEFAULT '',
        token_enc TEXT NOT NULL DEFAULT '',
        fingerprint TEXT NOT NULL DEFAULT '',
        status TEXT NOT NULL DEFAULT 'unknown',
        last_seen_at TEXT NULL,
        last_error TEXT NOT NULL DEFAULT '',
        agent_version TEXT NOT NULL DEFAULT '',
        created_at TEXT NOT NULL
    )
    SQL,
    "INSERT INTO servers (id, name, kind, created_at) VALUES (1, 'This server', 'local', strftime('%Y-%m-%dT%H:%M:%SZ','now'))",
    <<<'SQL'
    CREATE TABLE websites (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
        owner_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
        domain TEXT NOT NULL,
        system_user TEXT NOT NULL,
        root TEXT NOT NULL,
        document_root TEXT NOT NULL,
        kind TEXT NOT NULL,
        php_version TEXT NOT NULL DEFAULT '',
        php_settings TEXT NOT NULL DEFAULT '{}',
        proxy_pass TEXT NOT NULL DEFAULT '',
        https INTEGER NOT NULL DEFAULT 0,
        force_https INTEGER NOT NULL DEFAULT 0,
        hsts INTEGER NOT NULL DEFAULT 0,
        ssl_mode TEXT NOT NULL DEFAULT 'none',
        cert_file TEXT NOT NULL DEFAULT '',
        key_file TEXT NOT NULL DEFAULT '',
        redirects TEXT NOT NULL DEFAULT '[]',
        client_max_body_mb INTEGER NOT NULL DEFAULT 64,
        enabled INTEGER NOT NULL DEFAULT 1,
        status TEXT NOT NULL DEFAULT 'pending',
        last_error TEXT NOT NULL DEFAULT '',
        applied_at TEXT NULL,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        UNIQUE (server_id, domain)
    )
    SQL,
    <<<'SQL'
    CREATE TABLE domains (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
        website_id INTEGER NOT NULL REFERENCES websites(id) ON DELETE CASCADE,
        name TEXT NOT NULL,
        is_primary INTEGER NOT NULL DEFAULT 0,
        UNIQUE (server_id, name)
    )
    SQL,
    <<<'SQL'
    CREATE TABLE agent_jobs (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
        job_id TEXT NOT NULL,
        kind TEXT NOT NULL,
        target_type TEXT NOT NULL DEFAULT '',
        target_id TEXT NOT NULL DEFAULT '',
        user_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
        status TEXT NOT NULL DEFAULT 'queued',
        created_at TEXT NOT NULL,
        UNIQUE (server_id, job_id)
    )
    SQL,
    'ALTER TABLE discovery_snapshots ADD COLUMN server_id INTEGER NOT NULL DEFAULT 1',
    "ALTER TABLE roles ADD COLUMN scope TEXT NOT NULL DEFAULT 'all'",
    'ALTER TABLE users ADD COLUMN parent_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL',
];
