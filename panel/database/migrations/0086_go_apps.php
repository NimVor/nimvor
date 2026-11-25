<?php

declare(strict_types=1);

// Cloudreve and Gopeed in the app installer. They start in "testing" status, so only administrators can install
// them until a real-server run (.github/workflows/app-native.yml) has proven them and an administrator reviews them.
// INSERT OR IGNORE keeps any row an administrator already edited.

return [
    "INSERT OR IGNORE INTO app_catalog (slug, name, category_slug, description, icon, needs_db, sort, status, runtime, db_type) VALUES
        ('cloudreve', 'Cloudreve', 'files', 'Self-hosted file manager and cloud storage with sharing (a single program, SQLite).', 'bi-cloud-arrow-up', 0, 20, 'testing', 'binary', 'sqlite'),
        ('gopeed', 'Gopeed', 'files', 'Download manager (HTTP, BitTorrent, magnet links) with a web interface (a single program).', 'bi-download', 0, 30, 'testing', 'binary', 'none')",
];
