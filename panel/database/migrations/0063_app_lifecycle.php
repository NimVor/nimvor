<?php

declare(strict_types=1);

// App installer lifecycle: a catalog status (draft, testing, enabled,
// deprecated, disabled; every existing application stays enabled), the
// installed version and strategy of an installation, and at most one running
// installation per website folder.

return [
    "ALTER TABLE app_catalog ADD COLUMN status TEXT NOT NULL DEFAULT 'enabled'",
    "ALTER TABLE app_installs ADD COLUMN version TEXT NOT NULL DEFAULT ''",
    "ALTER TABLE app_installs ADD COLUMN strategy TEXT NOT NULL DEFAULT ''",
    // A stale duplicate (an older row still marked running for the same
    // folder) would stop the index from being created: keep the newest.
    "UPDATE app_installs SET status = 'unknown' WHERE status = 'running' AND id NOT IN (SELECT MAX(id) FROM app_installs WHERE status = 'running' GROUP BY website_id, path)",
    "CREATE UNIQUE INDEX app_installs_one_running ON app_installs (website_id, path) WHERE status = 'running'",
];
