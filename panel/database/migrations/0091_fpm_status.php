<?php

declare(strict_types=1);

// PHP-FPM pool status (an administrator's opt-in): workers in use, queue and saturation. NULL = the pool's status
// page was not on, or did not answer, at that sample.

return [
    'ALTER TABLE site_metric_samples ADD COLUMN fpm_active INTEGER NULL',
    'ALTER TABLE site_metric_samples ADD COLUMN fpm_idle INTEGER NULL',
    'ALTER TABLE site_metric_samples ADD COLUMN fpm_total INTEGER NULL',
    'ALTER TABLE site_metric_samples ADD COLUMN fpm_queue INTEGER NULL',      // requests waiting for a worker at the sample
    'ALTER TABLE site_metric_samples ADD COLUMN fpm_max_active INTEGER NULL', // most busy at once since PHP-FPM started
    'ALTER TABLE site_metric_samples ADD COLUMN fpm_hits INTEGER NULL',       // times pm.max_children was reached in the interval
    'ALTER TABLE site_metric_samples ADD COLUMN fpm_slow INTEGER NULL',       // slow requests in the interval
    'ALTER TABLE site_metric_samples ADD COLUMN fpm_accepted INTEGER NULL',   // requests the pool accepted in the interval
    'ALTER TABLE site_metric_hourly ADD COLUMN fpm_active_avg REAL NULL',
    'ALTER TABLE site_metric_hourly ADD COLUMN fpm_active_max INTEGER NULL',
    'ALTER TABLE site_metric_hourly ADD COLUMN fpm_queue_max INTEGER NULL',
    'ALTER TABLE site_metric_hourly ADD COLUMN fpm_hits INTEGER NULL',
    'ALTER TABLE site_metric_hourly ADD COLUMN fpm_slow INTEGER NULL',
];
