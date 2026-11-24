<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$settings = ['php_version' => '8.3', 'installed_versions' => ['8.2', '8.3', '8.4'], 'memory_limit_mb' => 256, 'upload_max_mb' => 64, 'max_execution_seconds' => 60, 'max_children' => 5, 'pm' => 'ondemand', 'start_servers' => 0, 'min_spare_servers' => 0, 'max_spare_servers' => 0, 'idle_timeout_seconds' => 10, 'max_requests' => 500, 'request_timeout_seconds' => 0, 'slowlog_seconds' => 10, 'harden' => true, 'lock_ini' => false, 'php_profile' => 'recommended', 'disable_functions' => '', 'ini' => ['display_errors' => 'Off', 'max_input_vars' => '5000']];
$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The website\'s id. Only PHP websites have PHP settings (`409` otherwise).', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$ref = O::ref('PhpSettings');
$admin = ' Administrators only.';
$props = [
    'php_version' => ['type' => 'string', 'description' => 'One of `installed_versions`. Changing it moves the website to that version\'s PHP-FPM.', 'examples' => ['8.3']],
    'memory_limit_mb' => ['type' => 'integer', 'minimum' => 32, 'maximum' => 8192],
    'upload_max_mb' => ['type' => 'integer', 'minimum' => 1, 'maximum' => 4096, 'description' => 'The largest upload (`upload_max_filesize`, `post_max_size` and the web server\'s body limit).'],
    'max_execution_seconds' => ['type' => 'integer', 'minimum' => 5, 'maximum' => 3600],
    'php_profile' => ['type' => 'string', 'enum' => ['', 'recommended', 'balanced', 'compatibility', 'custom'], 'description' => 'The security profile, which decides the functions that are disabled. `custom` uses `disable_functions`.' . $admin],
    'disable_functions' => ['type' => 'string', 'description' => 'With the `custom` profile: PHP functions to disable, separated by commas.' . $admin],
    'max_children' => ['type' => 'integer', 'minimum' => 1, 'maximum' => 200, 'description' => 'The most PHP processes the website may run at once.' . $admin],
    'pm' => ['type' => 'string', 'enum' => ['ondemand', 'dynamic', 'static'], 'description' => 'The PHP-FPM process manager.' . $admin],
    'start_servers' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 200, 'description' => 'For `dynamic`; 0 lets the server choose.' . $admin],
    'min_spare_servers' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 200, 'description' => 'For `dynamic`; 0 lets the server choose.' . $admin],
    'max_spare_servers' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 200, 'description' => 'For `dynamic`; 0 lets the server choose.' . $admin],
    'idle_timeout_seconds' => ['type' => 'integer', 'minimum' => 1, 'maximum' => 3600, 'description' => 'For `ondemand`: seconds before an idle process stops.' . $admin],
    'max_requests' => ['type' => 'integer', 'minimum' => 1, 'maximum' => 100000, 'description' => 'Requests a process serves before it is replaced.' . $admin],
    'request_timeout_seconds' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 86400, 'description' => 'Stops a request after this long; 0 means no limit.' . $admin],
    'slowlog_seconds' => ['type' => 'integer', 'minimum' => 1, 'maximum' => 3600, 'description' => 'Requests slower than this are written to the slow log.' . $admin],
    'harden' => ['type' => 'boolean', 'description' => 'Extra hardening of the PHP pool.' . $admin],
    'lock_ini' => ['type' => 'boolean', 'description' => 'Pin the plan\'s PHP limits so a `php.ini` or `.user.ini` cannot change them.' . $admin],
    'ini' => ['type' => 'object', 'additionalProperties' => ['type' => 'string'], 'description' => 'The website\'s own `php.ini` directives, as `name: value`. **Replaces the whole set** (send `{}` to clear it, or use `DELETE …/php/ini`). Only the directives of the panel\'s list are accepted; paths must lie inside the website\'s folder.', 'examples' => [['display_errors' => 'Off', 'max_input_vars' => '5000']]],
];

return [
    'tags' => [
        ['name' => 'PHP settings', 'description' => "The PHP version, limits and `php.ini` directives of a PHP website.\n\nA change writes the website's PHP-FPM pool, which is checked with `php-fpm -t` before it is reloaded; if the server refuses it the previous settings are restored. Process limits, hardening and the security profile are the administrator's: a hosting customer who sends them gets `422` naming the fields. The customer's own `php.ini` and `.user.ini` files are never changed by the API."],
    ],
    'schemas' => [
        'PhpSettings' => [
            'type' => 'object',
            'properties' => ['installed_versions' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'PHP versions with PHP-FPM installed on this server.']] + $props,
        ],
    ],
    'paths' => [
        '/websites/{id}/php' => [
            'get' => [
                'operationId' => 'getWebsitePhp',
                'tags' => ['PHP settings'],
                'summary' => 'Get a website\'s PHP settings',
                'description' => 'The stored settings, plus `effective`: what PHP really uses (from the pool, the website\'s `php.ini` and `.user.ini`, and where each value comes from), or null when the server cannot say.',
                'x-permission' => 'websites.view',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The settings.', ['type' => 'object', 'allOf' => [$ref], 'properties' => ['effective' => ['type' => ['object', 'null'], 'description' => 'The configuration PHP-FPM uses now.']]], $settings + ['effective' => null]), '404' => O::err('NotFound'), '409' => O::err('Conflict')],
            ],
            'patch' => [
                'operationId' => 'updateWebsitePhp',
                'tags' => ['PHP settings'],
                'summary' => 'Change a website\'s PHP settings',
                'description' => 'Only the fields you send change (except `ini`, which replaces the set). The website\'s other settings, domains and aliases stay as they are.',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'properties' => $props], ['php_version' => '8.4', 'memory_limit_mb' => 512, 'ini' => ['display_errors' => 'Off']]),
                'responses' => ['200' => O::res('The settings, changed.', $ref, ['php_version' => '8.4', 'memory_limit_mb' => 512] + $settings), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/php/reload' => ['post' => [
            'operationId' => 'reloadWebsitePhp',
            'tags' => ['PHP settings'],
            'summary' => 'Reload PHP-FPM',
            'description' => 'Reloads the PHP-FPM service of the website\'s PHP version (gracefully: running requests finish). It also reloads the other websites on that version.',
            'x-permission' => 'websites.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('Reloaded.', ['type' => 'object', 'properties' => ['reloaded' => ['type' => 'string']]], ['reloaded' => 'PHP 8.3']), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/php/check' => ['post' => [
            'operationId' => 'checkWebsitePhpConfig',
            'tags' => ['PHP settings'],
            'summary' => 'Check the website\'s php.ini now',
            'description' => 'Reads the website\'s own `php.ini` and `.user.ini` now (the server also notices changes within seconds) and reports whether PHP-FPM accepted them. `status` is `ok`, `pending` (a change is being applied) or `error` with a `message`.',
            'x-permission' => 'websites.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('The scan.', ['type' => 'object', 'properties' => ['status' => ['type' => 'string', 'enum' => ['ok', 'pending', 'error']], 'message' => ['type' => 'string']]], ['status' => 'ok']), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/php/ini' => ['delete' => [
            'operationId' => 'resetWebsitePhpIni',
            'tags' => ['PHP settings'],
            'summary' => 'Clear the directives set through the panel',
            'description' => 'Removes the `ini` directives set through the panel or the API. The customer\'s own `php.ini` and `.user.ini` files are not changed.',
            'x-permission' => 'websites.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('The settings, with `ini` empty.', $ref, ['ini' => (object) []] + $settings), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
    ],
];
