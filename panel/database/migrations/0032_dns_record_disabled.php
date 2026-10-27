<?php

declare(strict_types=1);

// DNS records can be turned off without deleting them (they are left out
// of the zone file).

return [
    'ALTER TABLE dns_records ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0',
];
