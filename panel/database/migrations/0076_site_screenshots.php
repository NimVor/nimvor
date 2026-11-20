<?php

declare(strict_types=1);

// The picture of a website's home page shown on its card: when it was taken
// (the picture itself is kept by the agent) and when it was last tried.

return [
    <<<'SQL'
    CREATE TABLE site_screenshots (
        website_id INTEGER PRIMARY KEY REFERENCES websites(id) ON DELETE CASCADE,
        taken_at TEXT NULL,
        attempted_at TEXT NOT NULL,
        error TEXT NOT NULL DEFAULT ''
    )
    SQL,
];
