<?php

declare(strict_types=1);

// FFmpeg per hosting plan: the accounts of plans with it may run the
// server's FFmpeg (members of the whacp-ffmpeg group). Off by default.

return [
    'ALTER TABLE plans ADD COLUMN ffmpeg INTEGER NOT NULL DEFAULT 0',
];
