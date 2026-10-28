<?php

declare(strict_types=1);

// Settings come from the environment; secrets can instead live in a file
// outside the web root that returns an array, named by SITE_CONFIG
// (for example /etc/nimvor-site/config.php). Nothing secret is in the repo.
$env = static fn (string $k, ?string $d = null): ?string => ($v = getenv($k)) === false || $v === '' ? $d : $v;

$config = [
    'env' => $env('SITE_ENV', 'production'),
    'url' => rtrim((string) $env('SITE_URL', 'https://nimvor.com'), '/'),
    'db' => [
        'host' => $env('DB_HOST', '127.0.0.1'),
        'port' => (int) $env('DB_PORT', '3306'),
        'name' => $env('DB_NAME', 'nimvor_site'),
        'user' => $env('DB_USER', 'nimvor_site'),
        'pass' => $env('DB_PASS', ''),
    ],
    // redis, memcached or file. Sessions, rate limits and cached pages live here.
    'cache' => [
        'driver' => $env('CACHE_DRIVER', 'redis'),
        'prefix' => $env('CACHE_PREFIX', 'nimsite:'),
        'redis' => ['host' => $env('REDIS_HOST', '127.0.0.1'), 'port' => (int) $env('REDIS_PORT', '6379'), 'password' => $env('REDIS_PASSWORD')],
        'memcached' => ['host' => $env('MEMCACHED_HOST', '127.0.0.1'), 'port' => (int) $env('MEMCACHED_PORT', '11211')],
        'file_dir' => $env('CACHE_DIR', dirname(__DIR__) . '/storage/cache'),
    ],
    'session_ttl' => (int) $env('SESSION_TTL', '7200'),
    'trusted_proxy' => $env('TRUSTED_PROXY', '127.0.0.1'),
];

if (($file = $env('SITE_CONFIG')) !== null && is_file($file)) {
    $config = array_replace_recursive($config, (array) require $file);
}

// Written by the first-run setup page (/setup); outside the web root, mode 0600.
if (is_file(__DIR__ . '/local.php')) {
    $config = array_replace_recursive($config, (array) require __DIR__ . '/local.php');
}

return $config;
