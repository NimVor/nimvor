-- NimVor website: upgrade the database to migration 0005 (Patreon sponsors, a per-sponsor "shown" switch).
--
-- For a site that was set up before this version and has no terminal access.
-- Run it ONCE, on the site's own database, with phpMyAdmin (Import, or the SQL tab) or your host's database tool.
-- It changes the `sponsors` table and records the migration so `bin/site migrate` skips it.
-- A new site needs none of this: the setup page creates everything. Running it twice stops with an error
-- ("duplicate column name"); that is harmless, nothing is changed by the second attempt.

ALTER TABLE sponsors MODIFY source ENUM('manual','github','patreon') NOT NULL DEFAULT 'manual';
ALTER TABLE sponsors ADD COLUMN patreon_id VARCHAR(40) NULL AFTER github_login, ADD COLUMN visible TINYINT(1) NOT NULL DEFAULT 1 AFTER monthly_usd, ADD UNIQUE KEY sponsors_patreon (patreon_id);

INSERT IGNORE INTO migrations (name, applied_at) VALUES ('0005_patreon_sponsors', UTC_TIMESTAMP());
