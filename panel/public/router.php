<?php

declare(strict_types=1);

// Router for PHP's built-in web server (development and the loopback-only
// nimvor-panel service). Serves existing static assets directly and sends
// everything else to index.php. Dotfiles and PHP files other than the front
// controller are never served.

$path = rawurldecode((string) parse_url((string) ($_SERVER['REQUEST_URI'] ?? '/'), PHP_URL_PATH));
$file = realpath(__DIR__ . $path);

if (
    $path !== '/'
    && $file !== false
    && str_starts_with($file, __DIR__ . '/assets/')
    && is_file($file)
    && !preg_match('#/\.#', $path)
) {
    return false;
}

require __DIR__ . '/index.php';
