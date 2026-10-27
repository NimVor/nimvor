<?php

declare(strict_types=1);

// Feature Manager, default-off features. Most features are on unless a list
// turns them off (feature_list_disabled). A few features are the other way
// round — off unless a list opts in (e.g. showing scheduled backups to the
// customer, or letting them create Node.js/Python/reverse-proxy sites). Those
// opt-ins live here, so they stay off for existing lists and for plans with no
// list at all.

return [
    <<<'SQL'
    CREATE TABLE feature_list_enabled (
        list_id INTEGER NOT NULL REFERENCES feature_lists(id) ON DELETE CASCADE,
        feature TEXT NOT NULL,
        PRIMARY KEY (list_id, feature)
    )
    SQL,
];
