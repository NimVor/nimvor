<?php

declare(strict_types=1);

// Per-user appearance: the customer panel theme, light/dark mode and accent colour. Empty means "use the
// administrator's default", so changing the default reaches everyone who never chose.
return [
    "ALTER TABLE users ADD COLUMN ui_theme TEXT NOT NULL DEFAULT ''",
    "ALTER TABLE users ADD COLUMN ui_mode TEXT NOT NULL DEFAULT ''",
    "ALTER TABLE users ADD COLUMN ui_accent TEXT NOT NULL DEFAULT ''",
];
