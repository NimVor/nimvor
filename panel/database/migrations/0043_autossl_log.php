<?php

declare(strict_types=1);

// AutoSSL log: what each check did for each website (issued, failed,
// waiting for DNS, queued), shown on SSL/TLS → AutoSSL.

return [
    'CREATE TABLE autossl_log (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL,
        website_id INTEGER NULL,
        domain TEXT NOT NULL,
        result TEXT NOT NULL,
        message TEXT NOT NULL DEFAULT \'\',
        created_at TEXT NOT NULL
    )',
    'CREATE INDEX autossl_log_server ON autossl_log (server_id, id)',
];
