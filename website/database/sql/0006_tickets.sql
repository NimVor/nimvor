-- NimVor website: upgrade the database to migration 0006 (support tickets).
--
-- For a site that was set up before this version and has no terminal access.
-- Run it ONCE, on the site's own database, with phpMyAdmin (Import, or the SQL tab) or your host's database tool.
-- It creates the ticket tables, copies the existing contact messages into them as tickets and records the
-- migration so `bin/site migrate` skips it. The contact_messages table is left as it is.
-- A new site needs none of this: the setup page creates everything. Running it twice stops with an error
-- ("table already exists"); that is harmless, nothing is changed by the second attempt.

CREATE TABLE tickets (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    ref CHAR(11) NOT NULL,
    access_token CHAR(32) NOT NULL,
    user_id BIGINT UNSIGNED NULL,
    name VARCHAR(120) NOT NULL,
    email VARCHAR(190) NOT NULL,
    subject VARCHAR(200) NOT NULL,
    category ENUM('general','support','sales','sponsorship','press','security','other') NOT NULL DEFAULT 'general',
    priority ENUM('low','normal','high','urgent') NOT NULL DEFAULT 'normal',
    status ENUM('open','answered','on_hold','resolved','closed') NOT NULL DEFAULT 'open',
    assigned_to BIGINT UNSIGNED NULL,
    first_response_at DATETIME NULL,
    closed_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE KEY tickets_ref (ref),
    KEY tickets_list (status, priority, updated_at),
    KEY tickets_user (user_id, updated_at),
    KEY tickets_assignee (assigned_to, status),
    KEY tickets_email (email),
    CONSTRAINT tickets_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL,
    CONSTRAINT tickets_assignee FOREIGN KEY (assigned_to) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE ticket_replies (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    ticket_id BIGINT UNSIGNED NOT NULL,
    user_id BIGINT UNSIGNED NULL,
    author ENUM('customer','staff','system') NOT NULL,
    body TEXT NOT NULL,
    internal TINYINT(1) NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    KEY replies_ticket (ticket_id, id),
    CONSTRAINT replies_ticket FOREIGN KEY (ticket_id) REFERENCES tickets (id) ON DELETE CASCADE,
    CONSTRAINT replies_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE ticket_canned (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    title VARCHAR(120) NOT NULL,
    body TEXT NOT NULL,
    created_at DATETIME NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO tickets (ref, access_token, user_id, name, email, subject, category, priority, status, closed_at, created_at, updated_at)
SELECT CONCAT('NV-O', LPAD(id, 7, '0')), MD5(CONCAT(UUID(), RAND(), email, created_at)), user_id, name, email, subject, 'general', 'normal',
       IF(status = 'closed', 'closed', 'open'), IF(status = 'closed', created_at, NULL), created_at, created_at
FROM contact_messages;

INSERT INTO ticket_replies (ticket_id, user_id, author, body, internal, created_at)
SELECT t.id, m.user_id, 'customer', m.message, 0, m.created_at
FROM contact_messages m JOIN tickets t ON t.ref = CONCAT('NV-O', LPAD(m.id, 7, '0'));

INSERT IGNORE INTO migrations (name, applied_at) VALUES ('0006_tickets', UTC_TIMESTAMP());
