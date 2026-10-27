<?php

declare(strict_types=1);

// A worker can run on a NimVor-managed runtime (e.g. node:20.17.0); empty
// means the system's own interpreters.

return [
    "ALTER TABLE workers ADD COLUMN runtime TEXT NOT NULL DEFAULT ''",
];
