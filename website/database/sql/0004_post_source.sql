-- NimVor website: upgrade the database to migration 0004 (posts remember the content file they came from).
--
-- For a site that was set up before this version and has no terminal access.
-- Run it ONCE, on the site's own database, with phpMyAdmin (Import, or the SQL tab) or your host's database tool.
-- It adds one column to `posts` and records the migration so `bin/site migrate` skips it.
-- A new site needs none of this: the setup page creates everything. Running it twice stops with an error
-- ("duplicate column name"); that is harmless, nothing is changed by the second attempt.

ALTER TABLE posts ADD COLUMN source_sha CHAR(40) NULL AFTER status;

INSERT IGNORE INTO migrations (name, applied_at) VALUES ('0004_post_source', UTC_TIMESTAMP());
