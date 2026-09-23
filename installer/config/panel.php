<?php

// WhaCP panel configuration (merged over panel/config/app.php).
// Owned by root, readable by the whacp group only.

return [
    'app' => [
        'env' => 'production',
        'debug' => false,
    ],
    'database' => ['path' => '/var/lib/whacp/whacp.sqlite'],
    'log' => ['file' => '/var/log/whacp/panel.log', 'level' => 'info'],
    'session' => [
        'path' => '/var/lib/whacp/sessions',
        // The panel listens on 127.0.0.1 over plain HTTP and is reached via an
        // SSH tunnel. Set to true as soon as it is served over HTTPS.
        'secure_cookie' => false,
    ],
    'agent' => [
        'socket' => '/run/whacp/agent.sock',
        'token_file' => '/etc/whacp/agent.token',
    ],
    'security' => [
        // Add your reverse proxy address here if you put one in front.
        'trusted_proxies' => [],
    ],
];
