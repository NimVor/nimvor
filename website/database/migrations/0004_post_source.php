<?php

declare(strict_types=1);

// Remembers which posts came from the site's own content folder (and which version), so an import
// updates those and never overwrites a post an administrator wrote.
return [
    'ALTER TABLE posts ADD COLUMN source_sha CHAR(40) NULL AFTER status',
];
