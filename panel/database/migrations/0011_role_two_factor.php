<?php

declare(strict_types=1);

// Roles can require two-factor sign-in.

return [
    'ALTER TABLE roles ADD COLUMN require_two_factor INTEGER NOT NULL DEFAULT 0',
];
