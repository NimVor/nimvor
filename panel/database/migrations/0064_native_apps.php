<?php

declare(strict_types=1);

// Native applications in the app installer: what an application runs on and
// how it is deployed (shown to customers), and the first native entries. They
// start in "testing" status, so only administrators can install them until
// they are proven on a real server and an administrator reviews them.

return [
    "ALTER TABLE app_catalog ADD COLUMN runtime TEXT NOT NULL DEFAULT 'php'",
    "ALTER TABLE app_catalog ADD COLUMN db_type TEXT NOT NULL DEFAULT ''",
    "ALTER TABLE app_catalog ADD COLUMN kind TEXT NOT NULL DEFAULT 'application'",
    "ALTER TABLE app_catalog ADD COLUMN deployment TEXT NOT NULL DEFAULT 'native'",
    "UPDATE app_catalog SET db_type = CASE WHEN needs_db = 1 THEN 'mysql' ELSE 'none' END WHERE db_type = ''",
    // WordPress with a plugin installed are extensions, not applications of their own.
    "UPDATE app_catalog SET kind = 'extension' WHERE slug IN ('woocommerce', 'tutor', 'learnpress', 'lifterlms', 'bbpress', 'buddypress', 'easy-digital-downloads', 'wp-statistics', 'koko-analytics', 'download-monitor')",
    "INSERT OR IGNORE INTO app_categories (slug, name, icon, sort) VALUES ('developer-tools', 'Developer Tools', 'bi-code-slash', 100)",
    "INSERT OR IGNORE INTO app_catalog (slug, name, category_slug, description, icon, needs_db, sort, status, runtime, db_type) VALUES
        ('gitea', 'Gitea', 'developer-tools', 'Self-hosted Git service: repositories, issues and pull requests (a single program, SQLite).', 'bi-git', 0, 10, 'testing', 'binary', 'sqlite'),
        ('django', 'Django', 'frameworks', 'Python web framework: a production project with the admin site (virtual environment, Gunicorn, SQLite).', 'bi-filetype-py', 0, 70, 'testing', 'python', 'sqlite'),
        ('fastapi', 'FastAPI', 'frameworks', 'Python API framework: a starter application (virtual environment, Uvicorn).', 'bi-lightning-charge', 0, 80, 'testing', 'python', 'none')",
];
