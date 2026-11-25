<?php

declare(strict_types=1);

// EspoCRM and SuiteCRM in the app installer. They start in "testing" status, so only administrators can install them
// until they are proven on a real server and an administrator reviews them. INSERT OR IGNORE keeps any row an
// administrator already edited.

return [
    "INSERT OR IGNORE INTO app_catalog (slug, name, category_slug, description, icon, needs_db, sort, status, runtime, db_type) VALUES
        ('espocrm', 'EspoCRM', 'business', 'Customer relationship management: contacts, sales, cases and a calendar.', 'bi-people', 1, 20, 'testing', 'php', 'mysql'),
        ('suitecrm', 'SuiteCRM', 'business', 'Open-source CRM for sales, marketing and support teams.', 'bi-person-lines-fill', 1, 30, 'testing', 'php', 'mysql')",
];
