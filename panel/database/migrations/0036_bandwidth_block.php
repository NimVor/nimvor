<?php

declare(strict_types=1);

// Monthly bandwidth enforcement: the month ("2026-09") in which an account
// went over its plan's data transfer. Its websites (and a reseller's
// customers' websites) answer 509 while it is the current month. Each
// website records whether the server has it that way (bandwidth_applied),
// so the scheduler re-applies the ones that differ on every server.

return [
    "ALTER TABLE users ADD COLUMN bw_blocked_month TEXT NOT NULL DEFAULT ''",
    'ALTER TABLE websites ADD COLUMN bandwidth_applied INTEGER NOT NULL DEFAULT 0',
];
