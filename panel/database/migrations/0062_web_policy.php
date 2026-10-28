<?php

declare(strict_types=1);

// Per-website security headers and cache rules chosen by the customer (JSON:
// {"headers": {...}, "cache": {...}}; empty = nothing customised). The agent
// renders them into the website's Nginx configuration.

return [
    "ALTER TABLE websites ADD COLUMN web_policy TEXT NOT NULL DEFAULT ''",
];
