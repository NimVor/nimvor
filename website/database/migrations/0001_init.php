<?php

declare(strict_types=1);

// All times are UTC. InnoDB and utf8mb4 throughout.
return [
    <<<'SQL'
    CREATE TABLE users (
        id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
        email VARCHAR(190) NOT NULL,
        name VARCHAR(120) NOT NULL,
        password_hash VARCHAR(255) NOT NULL,
        role ENUM('client','admin') NOT NULL DEFAULT 'client',
        is_active TINYINT(1) NOT NULL DEFAULT 1,
        created_at DATETIME NOT NULL,
        last_login_at DATETIME NULL,
        UNIQUE KEY users_email (email),
        KEY users_created (created_at)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
    SQL,
    <<<'SQL'
    CREATE TABLE posts (
        id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
        slug VARCHAR(120) NOT NULL,
        title VARCHAR(200) NOT NULL,
        excerpt VARCHAR(400) NOT NULL DEFAULT '',
        body MEDIUMTEXT NOT NULL,
        author_id BIGINT UNSIGNED NULL,
        status ENUM('draft','published') NOT NULL DEFAULT 'draft',
        published_at DATETIME NULL,
        created_at DATETIME NOT NULL,
        updated_at DATETIME NOT NULL,
        UNIQUE KEY posts_slug (slug),
        KEY posts_published (status, published_at),
        CONSTRAINT posts_author FOREIGN KEY (author_id) REFERENCES users (id) ON DELETE SET NULL
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
    SQL,
    <<<'SQL'
    CREATE TABLE contact_messages (
        id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
        user_id BIGINT UNSIGNED NULL,
        name VARCHAR(120) NOT NULL,
        email VARCHAR(190) NOT NULL,
        subject VARCHAR(200) NOT NULL,
        message TEXT NOT NULL,
        status ENUM('new','read','closed') NOT NULL DEFAULT 'new',
        created_at DATETIME NOT NULL,
        KEY messages_status (status, created_at),
        KEY messages_user (user_id),
        CONSTRAINT messages_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
    SQL,
    <<<'SQL'
    CREATE TABLE build_requests (
        id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
        user_id BIGINT UNSIGNED NULL,
        name VARCHAR(120) NOT NULL,
        email VARCHAR(190) NOT NULL,
        service VARCHAR(40) NOT NULL,
        details TEXT NOT NULL,
        status ENUM('new','quoted','in_progress','done','closed') NOT NULL DEFAULT 'new',
        created_at DATETIME NOT NULL,
        updated_at DATETIME NOT NULL,
        KEY requests_status (status, created_at),
        KEY requests_user (user_id),
        CONSTRAINT requests_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
    SQL,
    <<<'SQL'
    CREATE TABLE sponsors (
        id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
        name VARCHAR(120) NOT NULL,
        url VARCHAR(255) NOT NULL,
        tier ENUM('supporter','silver','gold') NOT NULL DEFAULT 'supporter',
        created_at DATETIME NOT NULL
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
    SQL,
    <<<'SQL'
    CREATE TABLE visits (
        day DATE NOT NULL,
        route VARCHAR(120) NOT NULL,
        hits INT UNSIGNED NOT NULL DEFAULT 0,
        PRIMARY KEY (day, route)
    ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4
    SQL,
];
