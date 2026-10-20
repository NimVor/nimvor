-- generated from panel/database/migrations/0053_app_catalog.php — do not edit by hand
CREATE TABLE app_categories (
    slug TEXT NOT NULL PRIMARY KEY,
    name TEXT NOT NULL,
    icon TEXT NOT NULL DEFAULT 'bi-grid',
    sort INTEGER NOT NULL DEFAULT 100,
    enabled INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE app_catalog (
    slug TEXT NOT NULL PRIMARY KEY,
    name TEXT NOT NULL,
    category_slug TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    icon TEXT NOT NULL DEFAULT 'bi-box',
    needs_db INTEGER NOT NULL DEFAULT 1,
    customer_visible INTEGER NOT NULL DEFAULT 1,
    enabled INTEGER NOT NULL DEFAULT 1,
    sort INTEGER NOT NULL DEFAULT 100
);

INSERT INTO app_categories (slug, name, icon, sort) VALUES
        ('blogs-cms', 'Blogs & CMS', 'bi-newspaper', 10),
        ('forums', 'Forums', 'bi-chat-left-text', 20),
        ('ecommerce', 'E-Commerce', 'bi-cart', 30),
        ('frameworks', 'Frameworks', 'bi-braces-asterisk', 40),
        ('files', 'Files & Collaboration', 'bi-folder', 50),
        ('education', 'Education', 'bi-mortarboard', 60),
        ('analytics', 'Analytics', 'bi-graph-up', 70);

INSERT INTO app_catalog (slug, name, category_slug, description, icon, needs_db, sort) VALUES
        ('wordpress', 'WordPress', 'blogs-cms', 'The world''s most popular blog and website builder.', 'bi-wordpress', 1, 10),
        ('joomla', 'Joomla', 'blogs-cms', 'Flexible content management system.', 'bi-journal-richtext', 1, 20),
        ('drupal', 'Drupal', 'blogs-cms', 'Powerful content management framework.', 'bi-droplet', 1, 30),
        ('grav', 'Grav', 'blogs-cms', 'Modern flat-file CMS — no database needed.', 'bi-feather', 0, 40),
        ('mediawiki', 'MediaWiki', 'blogs-cms', 'The wiki engine behind Wikipedia.', 'bi-book', 1, 50),
        ('phpbb', 'phpBB', 'forums', 'Popular open-source forum software.', 'bi-chat-left-text', 1, 10),
        ('smf', 'SMF', 'forums', 'Simple Machines Forum.', 'bi-chat-square-dots', 1, 20),
        ('prestashop', 'PrestaShop', 'ecommerce', 'Full-featured online store platform.', 'bi-bag', 1, 10),
        ('opencart', 'OpenCart', 'ecommerce', 'Lightweight e-commerce platform.', 'bi-cart', 1, 20),
        ('abantecart', 'AbanteCart', 'ecommerce', 'Free e-commerce platform.', 'bi-cart4', 1, 30),
        ('laravel', 'Laravel', 'frameworks', 'PHP application framework (via Composer).', 'bi-braces-asterisk', 1, 10),
        ('nextcloud', 'Nextcloud', 'files', 'Self-hosted files, calendar and collaboration.', 'bi-cloud', 1, 10),
        ('moodle', 'Moodle', 'education', 'Open-source learning management system.', 'bi-mortarboard', 1, 10),
        ('matomo', 'Matomo', 'analytics', 'Privacy-friendly web analytics.', 'bi-graph-up', 1, 10);

