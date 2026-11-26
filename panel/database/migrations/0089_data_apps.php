<?php

declare(strict_types=1);

// NocoDB and ClickHouse in the app installer. They start in "testing" status, so only administrators can install them
// until a real-server run (.github/workflows/app-native.yml) has proven them and an administrator reviews them.
// INSERT OR IGNORE keeps any row an administrator already edited.

return [
    "INSERT OR IGNORE INTO app_catalog (slug, name, category_slug, description, icon, needs_db, sort, status, runtime, db_type) VALUES
        ('nocodb', 'NocoDB', 'developer-tools', 'Spreadsheet-style interface over databases: tables, forms, views and APIs (a single program, SQLite).', 'bi-table', 0, 50, 'testing', 'binary', 'sqlite'),
        ('clickhouse', 'ClickHouse', 'developer-tools', 'Column database for analytics, with its web query page (a single program).', 'bi-bar-chart-steps', 0, 51, 'testing', 'binary', 'none')",
];
