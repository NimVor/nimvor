<?php

declare(strict_types=1);

// Per-account hosting options (DNS records for new websites, mail routing,
// spam filtering, …), set when the account is created or edited. Missing
// options take the defaults of AccountOptions.

return [
    <<<'SQL'
    CREATE TABLE account_options (
        user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
        options TEXT NOT NULL DEFAULT '{}',
        updated_at TEXT NOT NULL
    )
    SQL,
];
