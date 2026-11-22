<?php

declare(strict_types=1);

// Patreon sponsors next to GitHub's and the hand-added ones, and a per-sponsor "shown" switch.
return [
    "ALTER TABLE sponsors MODIFY source ENUM('manual','github','patreon') NOT NULL DEFAULT 'manual'",
    'ALTER TABLE sponsors ADD COLUMN patreon_id VARCHAR(40) NULL AFTER github_login, ADD COLUMN visible TINYINT(1) NOT NULL DEFAULT 1 AFTER monthly_usd, ADD UNIQUE KEY sponsors_patreon (patreon_id)',
];
