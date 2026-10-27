<?php

declare(strict_types=1);

// Built-in roles (except Administrator) can have their permissions changed
// by an administrator; a customised role keeps its grants on upgrades.

return [
    'ALTER TABLE roles ADD COLUMN customized INTEGER NOT NULL DEFAULT 0',
];
