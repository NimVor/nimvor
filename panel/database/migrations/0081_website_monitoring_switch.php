<?php

declare(strict_types=1);

// A website can be left out of the dashboard's uptime checks and their alerts
// (monitor_disabled = 1), for sites that are meant to be offline or unreachable
// from the server itself. The default is that every enabled website is checked.

return [
    'ALTER TABLE websites ADD COLUMN monitor_disabled INTEGER NOT NULL DEFAULT 0',
];
