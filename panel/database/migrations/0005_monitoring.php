<?php

declare(strict_types=1);

// Phase 17: persisted metrics and alerts.

return [
    <<<'SQL'
    CREATE TABLE metric_samples (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
        taken_at INTEGER NOT NULL,
        cpu REAL NOT NULL,
        mem REAL NOT NULL,
        disk REAL NOT NULL,
        load1 REAL NOT NULL,
        rx_bps REAL NOT NULL DEFAULT 0,
        tx_bps REAL NOT NULL DEFAULT 0
    )
    SQL,
    'CREATE INDEX metric_samples_time ON metric_samples (server_id, taken_at)',
    <<<'SQL'
    CREATE TABLE alerts (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1 REFERENCES servers(id),
        rule TEXT NOT NULL,
        subject TEXT NOT NULL DEFAULT '',
        severity TEXT NOT NULL,
        message TEXT NOT NULL,
        first_seen TEXT NOT NULL,
        last_seen TEXT NOT NULL,
        resolved_at TEXT NULL
    )
    SQL,
    'CREATE INDEX alerts_open ON alerts (server_id, resolved_at)',
];
