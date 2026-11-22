-- NimVor website: upgrade the database to migration 0007 (opt-in telemetry: installations, reports, release events).
--
-- For a site that was set up before this version and has no terminal access.
-- Run it ONCE, on the site's own database, with phpMyAdmin (Import, or the SQL tab) or your host's database tool.
-- It creates the telemetry tables and records the migration so `bin/site migrate` skips it.
-- A new site needs none of this: the setup page creates everything. Running it twice stops with an error
-- ("table already exists"); that is harmless, nothing is changed by the second attempt.

CREATE TABLE telemetry_installations (
    id INT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    installation_uuid CHAR(36) NOT NULL,
    token_hash CHAR(64) NOT NULL,
    token_confirmed TINYINT(1) NOT NULL DEFAULT 0,
    status ENUM('active','unregistered') NOT NULL DEFAULT 'active',
    schema_version SMALLINT UNSIGNED NOT NULL DEFAULT 1,
    nimvor_version VARCHAR(24) NOT NULL,
    agent_version VARCHAR(24) NOT NULL DEFAULT '',
    registered_at DATETIME NOT NULL,
    last_seen DATETIME NOT NULL,
    unregistered_at DATETIME NULL,
    UNIQUE KEY telemetry_installations_uuid (installation_uuid),
    KEY telemetry_installations_seen (last_seen),
    KEY telemetry_installations_version (nimvor_version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE telemetry_deleted (
    uuid_hash CHAR(64) NOT NULL PRIMARY KEY,
    deleted_at DATETIME NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE telemetry_heartbeats (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    installation_id INT UNSIGNED NOT NULL,
    event_id CHAR(36) NOT NULL,
    received_at DATETIME NOT NULL,
    nimvor_version VARCHAR(24) NOT NULL,
    agent_version VARCHAR(24) NOT NULL DEFAULT '',
    uptime_days SMALLINT UNSIGNED NULL,
    UNIQUE KEY telemetry_heartbeats_event (event_id),
    KEY telemetry_heartbeats_inst (installation_id, received_at),
    KEY telemetry_heartbeats_received (received_at),
    CONSTRAINT telemetry_heartbeats_fk FOREIGN KEY (installation_id) REFERENCES telemetry_installations (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE telemetry_os_inventory (
    installation_id INT UNSIGNED NOT NULL PRIMARY KEY,
    event_id CHAR(36) NOT NULL,
    captured_at DATETIME NOT NULL,
    os_id VARCHAR(24) NOT NULL,
    os_version VARCHAR(16) NOT NULL,
    os_family VARCHAR(16) NOT NULL,
    kernel_major VARCHAR(12) NOT NULL DEFAULT '',
    arch VARCHAR(12) NOT NULL,
    virtualization VARCHAR(24) NOT NULL DEFAULT 'unknown',
    init_system VARCHAR(16) NOT NULL DEFAULT 'unknown',
    cpu_cores SMALLINT UNSIGNED NULL,
    memory_mb_bucket INT UNSIGNED NULL,
    disk_gb_bucket INT UNSIGNED NULL,
    KEY telemetry_os_id (os_id, os_version),
    CONSTRAINT telemetry_os_fk FOREIGN KEY (installation_id) REFERENCES telemetry_installations (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE telemetry_software_inventory (
    installation_id INT UNSIGNED NOT NULL,
    component VARCHAR(40) NOT NULL,
    version VARCHAR(24) NOT NULL DEFAULT '',
    running TINYINT(1) NULL,
    captured_at DATETIME NOT NULL,
    PRIMARY KEY (installation_id, component),
    KEY telemetry_software_component (component, version),
    CONSTRAINT telemetry_software_fk FOREIGN KEY (installation_id) REFERENCES telemetry_installations (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE telemetry_feature_snapshots (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    installation_id INT UNSIGNED NOT NULL,
    event_id CHAR(36) NOT NULL,
    captured_at DATETIME NOT NULL,
    feature VARCHAR(40) NOT NULL,
    item_count INT UNSIGNED NOT NULL,
    UNIQUE KEY telemetry_feature_event (event_id, feature),
    KEY telemetry_feature_inst (installation_id, captured_at),
    KEY telemetry_feature_name (feature, captured_at),
    CONSTRAINT telemetry_feature_fk FOREIGN KEY (installation_id) REFERENCES telemetry_installations (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE telemetry_health_snapshots (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    installation_id INT UNSIGNED NOT NULL,
    event_id CHAR(36) NOT NULL,
    captured_at DATETIME NOT NULL,
    services_total SMALLINT UNSIGNED NULL,
    services_failed SMALLINT UNSIGNED NULL,
    disk_used_bucket TINYINT UNSIGNED NULL,
    memory_used_bucket TINYINT UNSIGNED NULL,
    updates_pending TINYINT(1) NULL,
    last_backup_ok TINYINT(1) NULL,
    ssl_expiring TINYINT(1) NULL,
    failed_jobs SMALLINT UNSIGNED NULL,
    UNIQUE KEY telemetry_health_event (event_id),
    KEY telemetry_health_inst (installation_id, captured_at),
    CONSTRAINT telemetry_health_fk FOREIGN KEY (installation_id) REFERENCES telemetry_installations (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE telemetry_release_events (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    installation_id INT UNSIGNED NOT NULL,
    event_id CHAR(36) NOT NULL,
    occurred_at DATETIME NOT NULL,
    kind ENUM('install','upgrade','downgrade','rollback') NOT NULL,
    from_version VARCHAR(24) NOT NULL DEFAULT '',
    to_version VARCHAR(24) NOT NULL,
    success TINYINT(1) NOT NULL,
    UNIQUE KEY telemetry_release_event (event_id),
    KEY telemetry_release_when (occurred_at),
    CONSTRAINT telemetry_release_fk FOREIGN KEY (installation_id) REFERENCES telemetry_installations (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE telemetry_api_events (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    received_at DATETIME NOT NULL,
    endpoint VARCHAR(24) NOT NULL,
    status SMALLINT UNSIGNED NOT NULL,
    code VARCHAR(40) NOT NULL DEFAULT '',
    schema_version SMALLINT UNSIGNED NULL,
    KEY telemetry_api_received (received_at),
    KEY telemetry_api_endpoint (endpoint, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO migrations (name, applied_at) VALUES ('0007_telemetry', UTC_TIMESTAMP());
