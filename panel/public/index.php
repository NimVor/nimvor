<?php

declare(strict_types=1);

use WhaCP\Core\Application;
use WhaCP\Http\Request;

require dirname(__DIR__) . '/vendor/autoload.php';

// Never leak PHP notices into responses; they go to the log via the error handler.
ini_set('display_errors', '0');
error_reporting(E_ALL);
set_error_handler(static function (int $severity, string $message, string $file, int $line): bool {
    if (!(error_reporting() & $severity)) {
        return false;
    }
    // Deprecations must not break the panel after a PHP upgrade.
    if ($severity === E_DEPRECATED || $severity === E_USER_DEPRECATED) {
        return true;
    }
    throw new ErrorException($message, 0, $severity, $file, $line);
});

Application::fromEnvironment()->handle(Request::fromGlobals())->send();
