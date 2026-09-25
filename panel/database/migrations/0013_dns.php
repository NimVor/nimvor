<?php

declare(strict_types=1);

// DNS zones served by the server's BIND, and their records.

return [
    <<<'SQL'
    CREATE TABLE dns_zones (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
        name TEXT NOT NULL,
        website_id INTEGER NULL REFERENCES websites(id) ON DELETE SET NULL,
        owner_id INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
        ttl INTEGER NOT NULL DEFAULT 3600,
        primary_ns TEXT NOT NULL,
        hostmaster TEXT NOT NULL,
        secondaries TEXT NOT NULL DEFAULT '',
        serial INTEGER NOT NULL DEFAULT 0,
        applied_at TEXT NULL,
        created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL,
        UNIQUE (server_id, name)
    )
    SQL,
    <<<'SQL'
    CREATE TABLE dns_records (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        zone_id INTEGER NOT NULL REFERENCES dns_zones(id) ON DELETE CASCADE,
        name TEXT NOT NULL,
        type TEXT NOT NULL,
        ttl INTEGER NOT NULL DEFAULT 0,
        value TEXT NOT NULL,
        priority INTEGER NOT NULL DEFAULT 0,
        weight INTEGER NOT NULL DEFAULT 0,
        port INTEGER NOT NULL DEFAULT 0
    )
    SQL,
    'CREATE INDEX dns_records_zone ON dns_records (zone_id)',
    'CREATE INDEX dns_zones_owner ON dns_zones (owner_id)',
];
