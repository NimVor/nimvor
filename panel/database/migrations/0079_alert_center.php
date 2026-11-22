<?php

declare(strict_types=1);

// Dashboard 2.0 alert center: acknowledging an alert and closing it by hand.
// An alert closed by hand that fires again within a day is reopened (still
// acknowledged) rather than raised as a new one.

return [
    'ALTER TABLE alerts ADD COLUMN acknowledged_at TEXT NULL',
    'ALTER TABLE alerts ADD COLUMN acknowledged_by INTEGER NULL',
    'ALTER TABLE alerts ADD COLUMN resolved_by INTEGER NULL',
];
