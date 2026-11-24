<?php

declare(strict_types=1);

// When a website's disk use was last asked for. The scheduler measures disk use at most hourly per website; before
// this column a website without a measured row (no folder on disk, a failed or timed-out measurement) made it
// measure every website again on every pass (every 5 minutes), which kept the agent busy walking all the files.
return [
    'ALTER TABLE websites ADD COLUMN usage_checked_at TEXT NULL',
];
