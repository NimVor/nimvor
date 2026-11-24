<?php

declare(strict_types=1);

// A cover image for each blog post (optional: without one the post uses the first picture in its text, or a
// generated cover), and the reviews shown on the home page and the Reviews page. Visitors' reviews wait for an
// administrator's approval; reviews copied from Trustpilot or written by hand are added under Admin.
return [
    "ALTER TABLE posts ADD COLUMN image VARCHAR(255) NOT NULL DEFAULT '' AFTER excerpt",
    <<<'SQL'
CREATE TABLE reviews (
    id INT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(120) NOT NULL,
    role VARCHAR(120) NOT NULL DEFAULT '',
    rating TINYINT UNSIGNED NOT NULL,
    title VARCHAR(160) NOT NULL DEFAULT '',
    body TEXT NOT NULL,
    source ENUM('visitor','trustpilot','manual') NOT NULL DEFAULT 'visitor',
    url VARCHAR(255) NOT NULL DEFAULT '',
    status ENUM('pending','published','hidden') NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL,
    KEY reviews_status (status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
SQL,
];
