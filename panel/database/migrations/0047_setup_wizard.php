<?php

declare(strict_types=1);

// The setup wizard opens once on an administrator's first dashboard visit.
// Panels already in use (with websites) are past that: it stays closed,
// reachable under Server → Setup wizard.

return [
    <<<'SQL'
    INSERT OR IGNORE INTO settings (key, value, updated_at)
    SELECT k, 'true', strftime('%Y-%m-%dT%H:%M:%S+00:00', 'now')
    FROM (SELECT 'setup_wizard_seen' AS k UNION ALL SELECT 'setup_wizard_done')
    WHERE EXISTS (SELECT 1 FROM websites)
    SQL,
];
