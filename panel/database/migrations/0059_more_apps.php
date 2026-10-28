<?php

declare(strict_types=1);

// More PHP applications for the app installer, with the categories they
// need. INSERT OR IGNORE keeps any rows an administrator already edited.

return [
    "INSERT OR IGNORE INTO app_categories (slug, name, icon, sort) VALUES
        ('business', 'Business, CRM & ERP', 'bi-briefcase', 80),
        ('finance', 'Finance & Invoicing', 'bi-receipt', 90)",
    "INSERT OR IGNORE INTO app_catalog (slug, name, category_slug, description, icon, needs_db, sort) VALUES
        ('backdrop', 'Backdrop CMS', 'blogs-cms', 'Lightweight CMS for small business and nonprofit sites.', 'bi-layout-text-window', 1, 60),
        ('processwire', 'ProcessWire', 'blogs-cms', 'Flexible, API-first content management system.', 'bi-diagram-3', 1, 70),
        ('bludit', 'Bludit', 'blogs-cms', 'Simple flat-file blogging CMS — no database needed.', 'bi-pencil-square', 0, 80),
        ('dolibarr', 'Dolibarr ERP & CRM', 'business', 'ERP and CRM for small and medium businesses.', 'bi-briefcase', 1, 10),
        ('akaunting', 'Akaunting', 'finance', 'Free online accounting and invoicing.', 'bi-receipt', 1, 10),
        ('typo3', 'TYPO3', 'blogs-cms', 'Enterprise content management system (via Composer).', 'bi-boxes', 1, 90),
        ('concrete', 'Concrete CMS', 'blogs-cms', 'CMS with in-context editing (via Composer).', 'bi-pencil', 1, 100),
        ('flarum', 'Flarum', 'forums', 'Modern, fast forum software (via Composer).', 'bi-chat-heart', 1, 30),
        ('bagisto', 'Bagisto', 'ecommerce', 'Laravel e-commerce platform (via Composer).', 'bi-shop', 1, 40),
        ('woocommerce', 'WooCommerce', 'ecommerce', 'WordPress with the WooCommerce online store plugin.', 'bi-cart-check', 1, 5),
        ('shopware', 'Shopware Community Edition', 'ecommerce', 'Open e-commerce platform (via Composer).', 'bi-basket', 1, 50)",
];
