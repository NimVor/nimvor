<?php

declare(strict_types=1);

// Per-website environment variables (administrator and customer). The values are secrets: the whole set is kept as
// one encrypted blob per website and is deleted with it. The agent writes them into the website's PHP-FPM pool
// (readable by root only) and the panel passes them to the website's background workers.

return [
    <<<'SQL'
    CREATE TABLE site_env (
        website_id INTEGER PRIMARY KEY REFERENCES websites(id) ON DELETE CASCADE,
        vars_enc TEXT NOT NULL,
        updated_at TEXT NOT NULL
    )
    SQL,
];
