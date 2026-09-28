<?php

declare(strict_types=1);

// Resource limits of hosting plans, applied to each account's processes
// with cgroups (0 = no limit): CPU (100 = one core), memory, processes and
// disk read/write speed.

return [
    'ALTER TABLE plans ADD COLUMN cpu_percent INTEGER NOT NULL DEFAULT 0',
    'ALTER TABLE plans ADD COLUMN memory_mb INTEGER NOT NULL DEFAULT 0',
    'ALTER TABLE plans ADD COLUMN tasks_max INTEGER NOT NULL DEFAULT 0',
    'ALTER TABLE plans ADD COLUMN io_read_mbps INTEGER NOT NULL DEFAULT 0',
    'ALTER TABLE plans ADD COLUMN io_write_mbps INTEGER NOT NULL DEFAULT 0',
];
