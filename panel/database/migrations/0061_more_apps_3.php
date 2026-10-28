<?php

declare(strict_types=1);

// A third batch: WordPress-based apps (a plugin installed on a new WordPress),
// Piwigo and Mautic. INSERT OR IGNORE keeps any rows an administrator edited.

return [
    "INSERT OR IGNORE INTO app_catalog (slug, name, category_slug, description, icon, needs_db, sort) VALUES
        ('tutor', 'Tutor LMS', 'education', 'Online course platform: courses, lessons and quizzes on WordPress.', 'bi-mortarboard-fill', 1, 30),
        ('learnpress', 'LearnPress', 'education', 'Learning management system plugin for WordPress.', 'bi-journal-bookmark', 1, 40),
        ('lifterlms', 'LifterLMS', 'education', 'Course, membership and quiz plugin for WordPress.', 'bi-award', 1, 50),
        ('bbpress', 'bbPress', 'forums', 'Forums for WordPress, from the makers of WordPress.', 'bi-chat-square-text', 1, 40),
        ('buddypress', 'BuddyPress', 'blogs-cms', 'Community and social network features for WordPress.', 'bi-people', 1, 130),
        ('easy-digital-downloads', 'Easy Digital Downloads', 'ecommerce', 'Sell digital downloads on WordPress.', 'bi-cloud-arrow-down', 1, 60),
        ('wp-statistics', 'WP Statistics', 'analytics', 'Privacy-friendly visitor statistics inside WordPress.', 'bi-bar-chart-line', 1, 20),
        ('koko-analytics', 'Koko Analytics', 'analytics', 'Simple, fast, privacy-friendly analytics for WordPress.', 'bi-activity', 1, 30),
        ('download-monitor', 'Download Monitor', 'files', 'Manage, track and sell file downloads on WordPress.', 'bi-download', 1, 20),
        ('piwigo', 'Piwigo', 'files', 'Photo gallery and media library.', 'bi-images', 1, 30),
        ('mautic', 'Mautic', 'business', 'Open-source marketing automation (via Composer).', 'bi-megaphone', 1, 30)",
];
