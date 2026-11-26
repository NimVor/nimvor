<?php

declare(strict_types=1);

// ClickHouse in the app installer. It starts in "testing" status, so only administrators can install it until an
// administrator has reviewed it (its real installation is proven by .github/workflows/app-native.yml). INSERT OR
// IGNORE keeps any row an administrator already edited.

return [
    "INSERT OR IGNORE INTO app_catalog (slug, name, category_slug, description, icon, needs_db, sort, status, runtime, db_type) VALUES
        ('clickhouse', 'ClickHouse', 'developer-tools', 'Column database for analytics, with its web query page (a single program).', 'bi-bar-chart-steps', 0, 51, 'testing', 'binary', 'none')",
];
