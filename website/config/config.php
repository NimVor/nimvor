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
    // Where the documentation, problems board and sponsors come from. The token is optional
    // (a higher rate limit) except for sponsors: GitHub only shows them to a signed-in maintainer.
    'github' => [
        'repo' => $env('GITHUB_REPO', 'NimVor/nimvor'),
        'branch' => $env('GITHUB_BRANCH', 'main'),
        'token' => $env('GITHUB_TOKEN', ''),
        'sponsorable' => $env('GITHUB_SPONSORABLE', 'NimVor'),
    ],
    // The check in front of the public forms: builtin (default, nothing leaves the site), turnstile, hcaptcha or off.
    // The two keys are only for turnstile / hcaptcha.
    'captcha' => [
        'provider' => $env('CAPTCHA_PROVIDER', 'builtin'),
        'site_key' => $env('CAPTCHA_SITE_KEY', ''),
        'secret_key' => $env('CAPTCHA_SECRET_KEY', ''),
    ],
    // Email for tickets: set MAIL_FROM (an address on this host) and customers get a copy of the replies; MAIL_NOTIFY
    // (comma-separated) lists who hears about new tickets and customer replies. Without MAIL_FROM nothing is sent
    // and tickets are followed in the browser only.
    'mail' => [
        'from' => $env('MAIL_FROM', ''),
        'name' => $env('MAIL_FROM_NAME', 'NimVor'),
        'notify' => $env('MAIL_NOTIFY', ''),
    ],
    // Fetch the documentation from GitHub on a visit when it is missing or older than six hours.
    'docs_auto_sync' => $env('DOCS_AUTO_SYNC', '1') === '1',
    // Uploaded videos live outside the web root. PHP's own upload_max_filesize and post_max_size
    // must be at least as large as max_mb (see the README).
    'videos' => [
        'dir' => $env('VIDEO_DIR', dirname(__DIR__) . '/storage/videos'),
        'max_mb' => (int) $env('VIDEO_MAX_MB', '500'),
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
