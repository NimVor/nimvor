<?php

declare(strict_types=1);

// Dashboard charts: more of what each monitoring pass measures is kept with the
// sample. Samples taken before this migration have NULL here (a gap in a chart),
// never a made-up value.

return [
    'ALTER TABLE metric_samples ADD COLUMN swap REAL NULL',          // swap used, percent (NULL: no swap or unknown)
    'ALTER TABLE metric_samples ADD COLUMN load5 REAL NULL',
    'ALTER TABLE metric_samples ADD COLUMN load15 REAL NULL',
    'ALTER TABLE metric_samples ADD COLUMN svc_up INTEGER NULL',     // enabled services running
    'ALTER TABLE metric_samples ADD COLUMN svc_total INTEGER NULL',  // enabled services
    'ALTER TABLE metric_samples ADD COLUMN db_conn INTEGER NULL',    // database connections
    'ALTER TABLE metric_samples ADD COLUMN db_qps REAL NULL',        // statements per second since the previous sample
    'ALTER TABLE metric_samples ADD COLUMN db_q INTEGER NULL',       // the server's statement counter (to compute db_qps)
];
