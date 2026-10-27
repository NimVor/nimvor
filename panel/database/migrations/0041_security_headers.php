<?php

declare(strict_types=1);

// Optional security headers per website (Referrer-Policy, X-Frame-Options,
// Permissions-Policy; upgrade-insecure-requests over HTTPS).

return [
    'ALTER TABLE websites ADD COLUMN security_headers INTEGER NOT NULL DEFAULT 0',
];
