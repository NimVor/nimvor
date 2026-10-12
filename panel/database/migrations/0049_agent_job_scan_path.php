<?php

declare(strict_types=1);

// An extract job remembers which folder it unpacked into, so the panel can
// scan the extracted files for malware once the job succeeds (scan-on-extract).

return [
    'ALTER TABLE agent_jobs ADD COLUMN scan_path TEXT NULL',
];
