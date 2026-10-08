<?php

declare(strict_types=1);

// Backup schedules for every website of the server ("all accounts"): a
// site schedule without a website. Each website gets its own backup, a
// few at a time; retention applies per website. (A column rather than a
// new kind: changing the CHECK constraint would mean rebuilding the
// table, which clears backups.schedule_id through its foreign key.)

return [
    'ALTER TABLE backup_schedules ADD COLUMN all_sites INTEGER NOT NULL DEFAULT 0',
];
