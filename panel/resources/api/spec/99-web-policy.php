<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The website\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$ref = O::ref('WebPolicy');
$headers = ['clickjacking' => 'sameorigin', 'frame_ancestors' => [], 'csp' => "default-src 'self'", 'csp_report_only' => false, 'referrer' => 'strict-origin-when-cross-origin', 'permissions' => 'restrictive', 'permissions_custom' => '', 'hsts' => ['enabled' => true, 'max_age' => 31536000, 'include_subdomains' => false, 'preload' => false]];
$cache = ['static_ttl' => 86400, 'dynamic_mode' => 'app', 'dynamic_ttl' => 60, 'bypass_paths' => ['/members'], 'page_cache' => ['enabled' => false, 'ttl' => 600], 'brotli' => true];
$state = ['headers' => $headers, 'cache' => $cache, 'headers_custom' => true, 'cache_custom' => true, 'hsts_available' => true];
$errs = ['404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')];

$headerProps = [
    'clickjacking' => ['type' => 'string', 'enum' => ['off', 'sameorigin', 'deny', 'allow'], 'description' => 'Who may embed the website\'s pages in a frame: nobody (`deny`), only itself (`sameorigin`, the default), itself and the sites in `frame_ancestors` (`allow`), or anyone (`off`).'],
    'frame_ancestors' => ['type' => 'array', 'items' => ['type' => 'string'], 'maxItems' => 10, 'description' => 'With `clickjacking` set to `allow`: 1 to 10 `https://` sites that may embed the website, such as `https://partner.example` or `https://*.example.org`. Otherwise it is ignored.'],
    'csp' => ['type' => 'string', 'maxLength' => 4000, 'description' => 'A Content-Security-Policy, such as `default-src \'self\'; img-src \'self\' https:`. Empty sends none. `frame-ancestors` is not accepted here (use `clickjacking`), a directive may appear once, and quotes other than `\'`, backslashes, `$`, braces, angle brackets and control characters are refused.'],
    'csp_report_only' => ['type' => 'boolean', 'description' => 'Send the policy as `Content-Security-Policy-Report-Only`: browsers report violations but block nothing. Only with a non-empty `csp`.'],
    'referrer' => ['type' => 'string', 'enum' => ['off', 'strict-origin-when-cross-origin', 'same-origin', 'no-referrer', 'strict-origin', 'origin-when-cross-origin', 'no-referrer-when-downgrade'], 'description' => 'The Referrer-Policy value (`off` sends none).'],
    'permissions' => ['type' => 'string', 'enum' => ['off', 'restrictive', 'strict', 'custom'], 'description' => 'The Permissions-Policy: a ready-made set (`restrictive`, `strict`), your own list (`custom`) or none (`off`).'],
    'permissions_custom' => ['type' => 'string', 'description' => 'With `permissions` set to `custom`: the list, such as `camera=(), geolocation=(self)`. Only known browser features are accepted.'],
    'hsts' => ['type' => 'object', 'description' => 'HTTP Strict-Transport-Security. Turning it on needs working HTTPS with plain HTTP redirected to it (`hsts_available`); a stale choice is never sent for a website without it. Send only the fields to change.', 'properties' => [
        'enabled' => ['type' => 'boolean'],
        'max_age' => ['type' => 'integer', 'minimum' => 300, 'maximum' => 63072000, 'description' => 'Seconds the browser remembers it; 5 minutes to 2 years. One year is recommended.'],
        'include_subdomains' => ['type' => 'boolean'],
        'preload' => ['type' => 'boolean', 'description' => 'Needs `include_subdomains` and at least a year.'],
    ]],
];
$cacheProps = [
    'static_ttl' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 31536000, 'description' => 'How long browsers keep static files (images, CSS, JavaScript, fonts), in seconds: 0 leaves it to the application, otherwise 60 seconds to one year.'],
    'dynamic_mode' => ['type' => 'string', 'enum' => ['app', 'revalidate', 'no-store', 'public'], 'description' => 'What NimVor adds to pages the application generates: nothing (`app`), `Cache-Control: no-cache` (`revalidate`), `no-store`, or a short public cache (`public`, only for pages without personal content).'],
    'dynamic_ttl' => ['type' => 'integer', 'minimum' => 30, 'maximum' => 3600, 'description' => 'With `dynamic_mode` `public`: seconds, 30 to one hour.'],
    'bypass_paths' => ['type' => 'array', 'items' => ['type' => 'string'], 'maxItems' => 20, 'description' => 'Paths that are never cached, such as `/members` (up to 20; letters, digits and `. _ ~ -` only). **Replaces the list.**'],
    'page_cache' => ['type' => 'object', 'description' => 'The Nginx page cache for PHP pages (PHP websites only). Send only the fields to change.', 'properties' => [
        'enabled' => ['type' => 'boolean'],
        'ttl' => ['type' => 'integer', 'enum' => [60, 300, 600, 1800, 3600, 21600, 86400], 'description' => 'Seconds a cached page is served.'],
    ]],
    'brotli' => ['type' => 'boolean', 'description' => 'Compress responses with Brotli for browsers that accept it. On by default.'],
];

return [
    'tags' => [
        ['name' => 'Security headers and cache', 'description' => "A website's browser security headers and cache rules.\n\nA change writes the website's web server configuration, which is checked before it is reloaded; if the server refuses it the previous settings are restored. Send only the fields to change: the others keep their current values, shown by `GET` (the recommended settings until a website saves its own). The server-level rules (`X-Content-Type-Options`, dotfile and PHP restrictions, request limits, ModSecurity) are not part of this and cannot be changed here."],
    ],
    'schemas' => [
        'WebPolicy' => ['type' => 'object', 'properties' => [
            'headers' => ['type' => 'object', 'properties' => $headerProps],
            'cache' => ['type' => 'object', 'properties' => $cacheProps],
            'headers_custom' => ['type' => 'boolean', 'description' => 'False while the website still uses the recommended headers.'],
            'cache_custom' => ['type' => 'boolean', 'description' => 'False while the website still uses the default cache rules.'],
            'hsts_available' => ['type' => 'boolean', 'description' => 'Whether HSTS can be turned on: the website has a certificate and HTTP is redirected to HTTPS.'],
        ]],
    ],
    'paths' => [
        '/websites/{id}/web-policy' => [
            'get' => [
                'operationId' => 'getWebsiteWebPolicy',
                'tags' => ['Security headers and cache'],
                'summary' => 'Get a website\'s security headers and cache rules',
                'description' => 'The saved settings, or the recommended ones until the website saves its own, plus `options`: the values each choice accepts.',
                'x-permission' => 'websites.view',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The settings.', ['type' => 'object', 'allOf' => [$ref], 'properties' => ['options' => ['type' => 'object', 'description' => 'The accepted values and limits of each choice.']]], $state + ['options' => ['clickjacking' => ['off', 'sameorigin', 'deny', 'allow'], 'dynamic_mode' => ['app', 'revalidate', 'no-store', 'public']]]), '404' => O::err('NotFound')],
            ],
            'delete' => [
                'operationId' => 'resetWebsiteWebPolicy',
                'tags' => ['Security headers and cache'],
                'summary' => 'Restore both sections to the defaults',
                'description' => 'Forgets the saved headers and cache rules and applies the recommended ones.',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The settings, back to the defaults.', $ref, ['headers_custom' => false, 'cache_custom' => false] + $state), '404' => O::err('NotFound')] + $errs,
            ],
        ],
        '/websites/{id}/web-policy/headers' => [
            'patch' => [
                'operationId' => 'updateWebsiteSecurityHeaders',
                'tags' => ['Security headers and cache'],
                'summary' => 'Change the security headers',
                'description' => 'Only the fields you send change. Unknown fields and values of the wrong kind are refused with `422`.',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'properties' => $headerProps], ['clickjacking' => 'deny', 'referrer' => 'no-referrer', 'hsts' => ['enabled' => true, 'max_age' => 31536000]]),
                'responses' => ['200' => O::res('The settings, changed.', $ref, $state), '422' => O::err('ValidationFailed')] + $errs,
            ],
            'delete' => [
                'operationId' => 'resetWebsiteSecurityHeaders',
                'tags' => ['Security headers and cache'],
                'summary' => 'Restore the security headers to the defaults',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The settings, with the headers back to the defaults.', $ref, ['headers_custom' => false] + $state)] + $errs,
            ],
        ],
        '/websites/{id}/web-policy/cache' => [
            'patch' => [
                'operationId' => 'updateWebsiteCacheRules',
                'tags' => ['Security headers and cache'],
                'summary' => 'Change the cache rules',
                'description' => 'Only the fields you send change (`bypass_paths` replaces its list). Unknown fields and values of the wrong kind are refused with `422`.',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'properties' => $cacheProps], ['static_ttl' => 86400, 'bypass_paths' => ['/members', '/cart'], 'page_cache' => ['enabled' => true, 'ttl' => 600]]),
                'responses' => ['200' => O::res('The settings, changed.', $ref, $state), '422' => O::err('ValidationFailed')] + $errs,
            ],
            'delete' => [
                'operationId' => 'resetWebsiteCacheRules',
                'tags' => ['Security headers and cache'],
                'summary' => 'Restore the cache rules to the defaults',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The settings, with the cache rules back to the defaults.', $ref, ['cache_custom' => false] + $state)] + $errs,
            ],
        ],
        '/websites/{id}/web-policy/cache/purge' => ['post' => [
            'operationId' => 'purgeWebsitePageCache',
            'tags' => ['Security headers and cache'],
            'summary' => 'Empty the page cache',
            'description' => 'Removes the cached PHP pages. Nothing else changes. `409` when the page cache is not turned on.',
            'x-permission' => 'websites.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('Emptied.', ['type' => 'object', 'properties' => ['purged' => ['type' => 'string', 'description' => 'The domain.']]], ['purged' => 'shop.example.com']), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
    ],
];
