<?php

declare(strict_types=1);

// Targeted malware scans (scan-on-upload, scan-on-extract, scan a file or
// folder) record which paths were scanned, relative to the website.

return [
    "ALTER TABLE av_scans ADD COLUMN paths TEXT NOT NULL DEFAULT '[]'",
];
