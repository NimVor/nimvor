<?php

declare(strict_types=1);

// Directory listing (nginx autoindex) per website: off unless turned on.

return [
    'ALTER TABLE websites ADD COLUMN directory_listing INTEGER NOT NULL DEFAULT 0',
];
