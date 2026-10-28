<?php

declare(strict_types=1);

// A second batch of PHP applications: frameworks (Composer starters), two
// CMSs, and more business and finance tools. INSERT OR IGNORE keeps any rows
// an administrator already edited.

return [
    "INSERT OR IGNORE INTO app_catalog (slug, name, category_slug, description, icon, needs_db, sort) VALUES
        ('symfony', 'Symfony', 'frameworks', 'PHP framework skeleton (via Composer).', 'bi-braces', 0, 20),
        ('codeigniter', 'CodeIgniter 4', 'frameworks', 'Lightweight PHP framework starter (via Composer).', 'bi-fire', 0, 30),
        ('yii', 'Yii 2', 'frameworks', 'High-performance PHP framework starter (via Composer).', 'bi-lightning', 0, 40),
        ('cakephp', 'CakePHP', 'frameworks', 'Rapid-development PHP framework starter (via Composer).', 'bi-cake2', 0, 50),
        ('slim', 'Slim', 'frameworks', 'PHP micro-framework skeleton (via Composer).', 'bi-sliders', 0, 60),
        ('kirby', 'Kirby', 'blogs-cms', 'Flat-file CMS — no database needed (via Composer).', 'bi-file-earmark-richtext', 0, 110),
        ('craft', 'Craft CMS', 'blogs-cms', 'Flexible content platform (via Composer).', 'bi-layers', 1, 120),
        ('invoiceninja', 'Invoice Ninja', 'finance', 'Invoicing, quotes, expenses and payments.', 'bi-receipt-cutoff', 1, 20),
        ('facturascripts', 'FacturaScripts', 'business', 'Accounting, invoicing and ERP.', 'bi-calculator', 1, 20)",
];
