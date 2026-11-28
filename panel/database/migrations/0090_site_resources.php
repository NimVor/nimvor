<?php

declare(strict_types=1);

// Website resource monitoring. Raw samples (one per monitoring pass) feed hourly roll-ups; both are pruned by
// retention settings. Every measured column is NULL when it was not measured (no PHP workers, no access log,
// first sample after an agent start): a chart shows a gap, never a made-up zero.

return [
    <<<'SQL'
    CREATE TABLE site_metric_samples (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1,
        website_id INTEGER NOT NULL REFERENCES websites(id) ON DELETE CASCADE,
        taken_at INTEGER NOT NULL,
        account TEXT NULL,            -- the limits account whose slice the website runs in
        interval_s REAL NOT NULL DEFAULT 0,
        workers INTEGER NULL,         -- PHP-FPM workers of the website's pool right now
        mem_bytes INTEGER NULL,       -- PSS of those workers right now
        cpu_s REAL NULL,              -- CPU seconds the workers used in the interval (sampled)
        read_b INTEGER NULL,          -- disk bytes read / written by the workers in the interval
        write_b INTEGER NULL,
        exited INTEGER NULL,          -- workers that ended between two samples: their last interval is missing
        req INTEGER NULL,             -- requests in the access log in the interval
        s2 INTEGER NULL, s3 INTEGER NULL, s4 INTEGER NULL, s5 INTEGER NULL,
        bytes_sent INTEGER NULL
    )
    SQL,
    'CREATE INDEX site_metric_samples_site ON site_metric_samples (website_id, taken_at)',
    'CREATE INDEX site_metric_samples_time ON site_metric_samples (taken_at)',
    <<<'SQL'
    CREATE TABLE site_metric_hourly (
        website_id INTEGER NOT NULL REFERENCES websites(id) ON DELETE CASCADE,
        bucket INTEGER NOT NULL,      -- start of the hour (unix)
        samples INTEGER NOT NULL,
        secs_php REAL NOT NULL DEFAULT 0,  -- seconds of interval in which the PHP workers were measured
        secs_web REAL NOT NULL DEFAULT 0,  -- ... and the access log
        cpu_s REAL NULL, read_b INTEGER NULL, write_b INTEGER NULL,
        mem_avg REAL NULL, mem_max INTEGER NULL, workers_max INTEGER NULL,
        req INTEGER NULL, s2 INTEGER NULL, s3 INTEGER NULL, s4 INTEGER NULL, s5 INTEGER NULL, bytes_sent INTEGER NULL,
        PRIMARY KEY (website_id, bucket)
    )
    SQL,
    'CREATE INDEX site_metric_hourly_time ON site_metric_hourly (bucket)',
    <<<'SQL'
    CREATE TABLE account_metric_samples (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1,
        account TEXT NOT NULL,
        taken_at INTEGER NOT NULL,
        interval_s REAL NOT NULL DEFAULT 0,
        mem_bytes INTEGER NULL, mem_peak INTEGER NULL, tasks INTEGER NULL,
        mem_limit_mb INTEGER NULL, cpu_limit_pct INTEGER NULL, tasks_limit INTEGER NULL,
        cpu_s REAL NULL, throttled_s REAL NULL, nr_throttled INTEGER NULL,
        mem_high INTEGER NULL, mem_max INTEGER NULL, oom_kills INTEGER NULL,
        io_r INTEGER NULL, io_w INTEGER NULL, io_rios INTEGER NULL, io_wios INTEGER NULL
    )
    SQL,
    'CREATE INDEX account_metric_samples_acct ON account_metric_samples (account, taken_at)',
    // The previous cumulative counters of an account's slice, to turn them into the interval's deltas.
    <<<'SQL'
    CREATE TABLE account_metric_state (
        server_id INTEGER NOT NULL DEFAULT 1,
        account TEXT NOT NULL,
        taken_at INTEGER NOT NULL,
        counters TEXT NOT NULL,
        PRIMARY KEY (server_id, account)
    )
    SQL,
    <<<'SQL'
    CREATE TABLE site_resource_events (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        server_id INTEGER NOT NULL DEFAULT 1,
        account TEXT NOT NULL,
        website_id INTEGER NULL REFERENCES websites(id) ON DELETE CASCADE,
        kind TEXT NOT NULL,
        severity TEXT NOT NULL,       -- fault: a limit was actually hit; warning: a threshold was crossed
        first_at INTEGER NOT NULL,
        last_at INTEGER NOT NULL,
        occurrences INTEGER NOT NULL DEFAULT 1,
        detail TEXT NOT NULL DEFAULT ''
    )
    SQL,
    'CREATE INDEX site_resource_events_acct ON site_resource_events (account, last_at)',
    'CREATE INDEX site_resource_events_site ON site_resource_events (website_id, last_at)',
];
