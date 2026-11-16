-- NimVor website: upgrade the database to migration 0003 (the Videos page).
--
-- For a site that was set up before this version and has no terminal access.
-- Run it ONCE, on the site's own database, with phpMyAdmin (Import, or the SQL tab) or your host's database tool.
-- It adds one table, `videos`, and records the migration so `bin/site migrate` skips it.
-- A new site needs none of this: the setup page creates everything. Running it twice stops with an error
-- ("table already exists"); that is harmless, nothing is changed by the second attempt.

CREATE TABLE videos (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    slug VARCHAR(120) NOT NULL,
    title VARCHAR(160) NOT NULL,
    description TEXT NOT NULL,
    category VARCHAR(40) NOT NULL DEFAULT 'General',
    source ENUM('youtube','upload') NOT NULL,
    youtube_id CHAR(11) NULL,
    file VARCHAR(80) NULL,
    mime VARCHAR(20) NULL,
    size_bytes BIGINT UNSIGNED NULL,
    poster VARCHAR(80) NULL,
    duration VARCHAR(12) NOT NULL DEFAULT '',
    sort_order INT NOT NULL DEFAULT 100,
    published TINYINT(1) NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    UNIQUE KEY videos_slug (slug),
    KEY videos_list (published, category, sort_order)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO migrations (name, applied_at) VALUES ('0003_videos', UTC_TIMESTAMP());
