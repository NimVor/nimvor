<?php

declare(strict_types=1);

// Adminer and phpMyAdmin in the app installer, as database tools on a website of their own. They start in "testing"
// status, so only administrators can install them until they are proven on a real server and an administrator
// reviews them. INSERT OR IGNORE keeps any row an administrator already edited.

return [
    "INSERT OR IGNORE INTO app_catalog (slug, name, category_slug, description, icon, needs_db, sort, status, runtime, db_type) VALUES
        ('adminer', 'Adminer', 'developer-tools', 'Manage MySQL, MariaDB, PostgreSQL and SQLite databases from one small PHP file.', 'bi-database-gear', 0, 40, 'testing', 'php', 'mysql'),
        ('phpmyadmin', 'phpMyAdmin', 'developer-tools', 'The classic web interface for MySQL and MariaDB databases.', 'bi-database', 0, 41, 'testing', 'php', 'mysql')",
];
