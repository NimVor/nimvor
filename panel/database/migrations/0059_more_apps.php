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
        ('akaunting', 'Akaunting', 'finance', 'Free online accounting and invoicing.', 'bi-receipt', 1, 10)",
];
