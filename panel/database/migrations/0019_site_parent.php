<?php

declare(strict_types=1);

// Subdomains and addon domains are websites created from another website
// (same account); parent_id remembers which. Deleting the parent keeps them.

return [
    'ALTER TABLE websites ADD COLUMN parent_id INTEGER NULL REFERENCES websites(id) ON DELETE SET NULL',
];
