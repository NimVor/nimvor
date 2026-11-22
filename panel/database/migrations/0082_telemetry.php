<?php

declare(strict_types=1);

// Opt-in telemetry (docs/telemetry/README.md). Nothing is created or sent until an administrator turns it on.
// telemetry_state: the choice, the random installation id, the (encrypted) token of the central interface and
// when each report last went out. telemetry_queue: reports waiting to be sent, retried with back-off.

return [
    'CREATE TABLE telemetry_state (
        key TEXT NOT NULL PRIMARY KEY,
        value TEXT NOT NULL,
        updated_at TEXT NOT NULL
    )',
    'CREATE TABLE telemetry_queue (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        endpoint TEXT NOT NULL,
        event_id TEXT NOT NULL UNIQUE,
        payload TEXT NOT NULL,
        attempts INTEGER NOT NULL DEFAULT 0,
        next_attempt_at TEXT NOT NULL,
        last_error TEXT NOT NULL DEFAULT \'\',
        created_at TEXT NOT NULL
    )',
    'CREATE INDEX telemetry_queue_due ON telemetry_queue (next_attempt_at)',
];
