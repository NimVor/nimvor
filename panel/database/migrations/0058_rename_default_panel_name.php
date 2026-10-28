<?php

declare(strict_types=1);

// The project is now called NimVor. A panel whose name was never changed
// (or was saved as the old default) falls back to the new default; a name an
// administrator chose themselves is kept.

return [
    "DELETE FROM settings WHERE key = 'panel_name' AND value = 'NimVor'",
];
