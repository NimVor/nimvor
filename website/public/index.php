<?php

declare(strict_types=1);

// Development only (php -S ... public/index.php): serve real files such as assets directly.
if (PHP_SAPI === 'cli-server') {
    $file = __DIR__ . rawurldecode((string) parse_url((string) $_SERVER['REQUEST_URI'], PHP_URL_PATH));
    if (is_file($file) && realpath($file) !== __FILE__ && str_starts_with((string) realpath($file), __DIR__ . '/assets/')) {
        return false;
    }
}

require dirname(__DIR__) . '/app/bootstrap.php';

use NimVorSite\Core\App;
use NimVorSite\Core\Request;

try {
    $config = require dirname(__DIR__) . '/config/config.php';
    $app = App::boot($config, dirname(__DIR__));
} catch (Throwable $e) {
    error_log('nimvor-site boot: ' . $e->getMessage());
    http_response_code(503);
    header('Content-Type: text/plain; charset=UTF-8');
    echo "The site is temporarily unavailable. Please try again in a moment.\n";
    exit;
}

$app->handle(Request::fromGlobals())->send();
