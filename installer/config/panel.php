<?php

// NimVor panel configuration (merged over panel/config/app.php).
// Owned by root, readable by the nimvor group only.

return [
    'app' => [
        'env' => 'production',
        'debug' => false,
    ],
    'database' => ['path' => '/var/lib/nimvor/nimvor.sqlite'],
    'log' => ['file' => '/var/log/nimvor/panel.log', 'level' => 'info'],
    'session' => [
        'path' => '/var/lib/nimvor/sessions',
        // The panel listens on 127.0.0.1 over plain HTTP and is reached via an
        // SSH tunnel. Set to true as soon as it is served over HTTPS.
        'secure_cookie' => false,
    ],
    'agent' => [
        'socket' => '/run/nimvor/agent.sock',
        'token_file' => '/etc/nimvor/agent.token',
    ],
    'security' => [
        // Add your reverse proxy address here if you put one in front.
        'trusted_proxies' => [],
        'key_file' => '/etc/nimvor/panel.key',
    ],
];
