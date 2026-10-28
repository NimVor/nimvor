<?php

declare(strict_types=1);

// Loads the site's classes without Composer, so the site runs from a plain
// checkout (Composer is only needed for the tests).
spl_autoload_register(static function (string $class): void {
    $prefix = 'NimVorSite\\';
    if (!str_starts_with($class, $prefix)) {
        return;
    }
    $file = __DIR__ . '/' . str_replace('\\', '/', substr($class, strlen($prefix))) . '.php';
    if (is_file($file)) {
        require $file;
    }
});

require_once __DIR__ . '/Support/helpers.php';
