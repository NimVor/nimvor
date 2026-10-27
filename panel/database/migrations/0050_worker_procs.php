<?php

declare(strict_types=1);

// A worker can run several identical copies (e.g. a few queue workers).
// procs is how many; existing workers keep a single copy.

return [
    'ALTER TABLE workers ADD COLUMN procs INTEGER NOT NULL DEFAULT 1',
];
