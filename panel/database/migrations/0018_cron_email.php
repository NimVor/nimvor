<?php

declare(strict_types=1);

// A cron job can have its output mailed; the address is taken from the
// account of whoever turned it on ('' = not mailed).

return [
    "ALTER TABLE cron_jobs ADD COLUMN email TEXT NOT NULL DEFAULT ''",
];
