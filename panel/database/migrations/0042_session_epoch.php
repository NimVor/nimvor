<?php

declare(strict_types=1);

// "Sign out other sessions": raising the epoch ends every session of the
// user except the one that raised it (it is part of the session binding).

return [
    'ALTER TABLE users ADD COLUMN session_epoch INTEGER NOT NULL DEFAULT 0',
];
