<?php

declare(strict_types=1);

// Per-account limit overrides on top of the hosting plan (JSON: plan
// column => value, e.g. {"disk_quota_mb": 25600}); the plan is unchanged.

return [
    "ALTER TABLE users ADD COLUMN limit_overrides TEXT NOT NULL DEFAULT '{}'",
];
