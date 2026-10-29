<?php

declare(strict_types=1);

// Documentation pages synced from GitHub, GitHub-sourced sponsors, install statistics.
return [
    <<<'SQL'
    CREATE TABLE site_state (
        k VARCHAR(60) NOT NULL PRIMARY KEY,
        v MEDIUMTEXT NOT NULL,
        updated_at DATETIME NOT NULL
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
    SQL,
    <<<'SQL'
    CREATE TABLE docs_pages (
        id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
        slug VARCHAR(160) NOT NULL,
        source_path VARCHAR(190) NOT NULL,
        sha CHAR(40) NOT NULL,
        grp VARCHAR(40) NOT NULL,
        sort_order INT NOT NULL DEFAULT 900,
        label VARCHAR(80) NOT NULL,
        title VARCHAR(200) NOT NULL,
        body MEDIUMTEXT NOT NULL,
        html MEDIUMTEXT NOT NULL,
        toc TEXT NOT NULL,
        render_v SMALLINT UNSIGNED NOT NULL DEFAULT 0,
        synced_at DATETIME NOT NULL,
        UNIQUE KEY docs_slug (slug),
        UNIQUE KEY docs_source (source_path),
        KEY docs_order (grp, sort_order)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
    SQL,
    "ALTER TABLE sponsors ADD COLUMN source ENUM('manual','github') NOT NULL DEFAULT 'manual' AFTER tier, ADD COLUMN github_login VARCHAR(80) NULL AFTER source, ADD COLUMN monthly_usd INT UNSIGNED NULL AFTER github_login, ADD UNIQUE KEY sponsors_login (github_login)",
    // One row per server that reported an installation. Nothing identifies the
    // server or its owner: a random id the installer made, and what is shown below.
    <<<'SQL'
    CREATE TABLE installs (
        install_id CHAR(32) NOT NULL PRIMARY KEY,
        first_seen DATETIME NOT NULL,
        last_seen DATETIME NOT NULL,
        version VARCHAR(24) NOT NULL,
        os VARCHAR(16) NOT NULL,
        os_version VARCHAR(12) NOT NULL,
        arch VARCHAR(8) NOT NULL,
        pings INT UNSIGNED NOT NULL DEFAULT 1,
        KEY installs_first (first_seen),
        KEY installs_last (last_seen)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
    SQL,
];
