<?php

declare(strict_types=1);

// A database user created for one website belongs to that website: the other websites of the same
// customer no longer list it (the list on a website's page shows the users of that website only).

return [
    'ALTER TABLE database_users ADD COLUMN website_id INTEGER NULL REFERENCES websites(id) ON DELETE SET NULL',
    // Users that already have grants on the databases of exactly one website belong to it.
    <<<'SQL'
    UPDATE database_users SET website_id = (
        SELECT MIN(d.website_id) FROM database_grants g JOIN databases d ON d.id = g.database_id
        WHERE g.user_id = database_users.id AND d.website_id IS NOT NULL
        GROUP BY g.user_id HAVING COUNT(DISTINCT d.website_id) = 1
    ) WHERE website_id IS NULL
    SQL,
];
