<?php

declare(strict_types=1);

// Videos shown on the Videos page: a YouTube link or a file uploaded under Admin.
return [
    <<<'SQL'
    CREATE TABLE videos (
        id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
        slug VARCHAR(120) NOT NULL,
        title VARCHAR(160) NOT NULL,
        description TEXT NOT NULL,
        category VARCHAR(40) NOT NULL DEFAULT 'General',
        source ENUM('youtube','upload') NOT NULL,
        youtube_id CHAR(11) NULL,
        file VARCHAR(80) NULL,
        mime VARCHAR(20) NULL,
        size_bytes BIGINT UNSIGNED NULL,
        poster VARCHAR(80) NULL,
        duration VARCHAR(12) NOT NULL DEFAULT '',
        sort_order INT NOT NULL DEFAULT 100,
        published TINYINT(1) NOT NULL DEFAULT 1,
        created_at DATETIME NOT NULL,
        UNIQUE KEY videos_slug (slug),
        KEY videos_list (published, category, sort_order)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
    SQL,
];
