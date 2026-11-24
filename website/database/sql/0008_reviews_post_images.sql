-- NimVor website: upgrade the database to migration 0008 (blog cover images, reviews).
--
-- For a site that was set up before this version and has no terminal access.
-- Run it ONCE, on the site's own database, with phpMyAdmin (Import, or the SQL tab) or your host's database tool.
-- It adds a column to `posts`, creates the `reviews` table and records the migration so `bin/site migrate` skips it.
-- A new site needs none of this: the setup page creates everything. Running it twice stops with an error
-- ("duplicate column name"); that is harmless, nothing is changed by the second attempt.

ALTER TABLE posts ADD COLUMN image VARCHAR(255) NOT NULL DEFAULT '' AFTER excerpt;

CREATE TABLE reviews (
    id INT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(120) NOT NULL,
    role VARCHAR(120) NOT NULL DEFAULT '',
    rating TINYINT UNSIGNED NOT NULL,
    title VARCHAR(160) NOT NULL DEFAULT '',
    body TEXT NOT NULL,
    source ENUM('visitor','trustpilot','manual') NOT NULL DEFAULT 'visitor',
    url VARCHAR(255) NOT NULL DEFAULT '',
    status ENUM('pending','published','hidden') NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL,
    KEY reviews_status (status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT IGNORE INTO migrations (name, applied_at) VALUES ('0008_reviews_post_images', UTC_TIMESTAMP());
