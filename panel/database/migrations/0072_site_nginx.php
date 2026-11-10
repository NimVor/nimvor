<?php

declare(strict_types=1);

// Per-website Nginx tuning chosen by an administrator (JSON: timeouts, buffers,
// compression, a few allowlisted directives; empty = the server-wide values
// apply). The agent validates it again and renders it into the website's
// Nginx configuration.

return [
    "ALTER TABLE websites ADD COLUMN nginx_settings TEXT NOT NULL DEFAULT ''",
];
