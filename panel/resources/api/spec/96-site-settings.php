<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The website\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$settings = ['domain' => 'shop.example.com', 'kind' => 'static', 'aliases' => ['www.shop.example.com'], 'document_root' => '/home/nimvor/sites/shop.example.com/public_html', 'document_root_sub' => 'public_html', 'client_max_body_mb' => 64, 'proxy_pass' => '', 'directory_listing' => false, 'monitoring' => true];
$ref = O::ref('SiteSettings');
$env = ['names' => ['APP_ENV', 'STRIPE_KEY'], 'count' => 2, 'max' => 100];
$runtime = ['type' => 'nodejs', 'app_runtime' => 'node:20.1.0', 'available' => ['node:20.1.0', 'node:22.0.0'], 'proxy_pass' => 'http://127.0.0.1:3000'];
$perm = ['repaired' => false, 'checked' => 120, 'wrong' => 3, 'fixed' => 0, 'skipped' => 1];
$errs = ['404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')];

$props = [
    'aliases' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'Other names the website answers to. **Replaces the whole list** (send `[]` to remove them all). Each name must not belong to another website; the plan\'s limit for parked domains applies.'],
    'document_root_sub' => ['type' => 'string', 'description' => 'The folder the website is served from, relative to its home folder, such as `public_html/public`. Send `public_html` to go back to the default.', 'examples' => ['public_html/public']],
    'client_max_body_mb' => ['type' => 'integer', 'minimum' => 1, 'maximum' => 4096, 'description' => 'The largest request body (upload) the web server accepts, in megabytes.'],
    'proxy_pass' => ['type' => 'string', 'description' => 'Proxy websites only: the local address the web server forwards to, such as `http://127.0.0.1:3000`.'],
    'directory_listing' => ['type' => 'boolean', 'description' => 'Folders without an index page list their files. Not available for proxy websites.'],
    'monitoring' => ['type' => 'boolean', 'description' => 'Include the website in the uptime checks and alerts. Nothing changes on the server.'],
];

return [
    'tags' => [
        ['name' => 'Website settings', 'description' => "A website's own settings and tools: names, folder and limits, re-applying its configuration, file permissions, environment variables and the runtime of a Node.js or Python website.\n\nA change that touches the web server configuration is checked before it is reloaded, and undone when the server refuses it. Only the fields you send change. **Environment variable values are write-only**: the API lists the names, never the values, and audit records carry the names only."],
    ],
    'schemas' => [
        'SitePermissions' => ['type' => 'object', 'properties' => [
            'repaired' => ['type' => 'boolean', 'description' => 'False for a check, true for a repair.'],
            'checked' => ['type' => 'integer', 'description' => 'Files and folders looked at.'], 'wrong' => ['type' => 'integer', 'description' => 'Those with the wrong owner or mode.'],
            'fixed' => ['type' => 'integer', 'description' => 'Those repaired (0 for a check).'], 'skipped' => ['type' => 'integer', 'description' => 'Links and special files left as they are.'],
        ]],
        'SiteEnv' => ['type' => 'object', 'properties' => ['names' => ['type' => 'array', 'items' => ['type' => 'string']], 'count' => ['type' => 'integer'], 'max' => ['type' => 'integer'], 'note' => ['type' => 'string']]],
        'SiteRuntime' => ['type' => 'object', 'properties' => [
            'type' => ['type' => 'string', 'enum' => ['nodejs', 'python']], 'app_runtime' => ['type' => 'string'],
            'available' => ['type' => 'array', 'items' => ['type' => 'string']], 'proxy_pass' => ['type' => 'string'],
        ]],
        'SiteSettings' => [
            'type' => 'object',
            'properties' => [
                'domain' => ['type' => 'string'], 'kind' => ['type' => 'string', 'description' => 'The website type; Node.js and Python websites are `proxy` websites bound to a runtime.'],
                'document_root' => ['type' => 'string', 'description' => 'The full path the website is served from.'],
            ] + $props,
        ],
    ],
    'paths' => [
        '/websites/{id}/settings' => [
            'get' => [
                'operationId' => 'getWebsiteSettings',
                'tags' => ['Website settings'],
                'summary' => 'Get a website\'s settings',
                'description' => 'Names, folder, upload limit, proxy address and the two toggles.',
                'x-permission' => 'websites.view',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The settings.', $ref, $settings), '404' => O::err('NotFound')],
            ],
            'patch' => [
                'operationId' => 'updateWebsiteSettings',
                'tags' => ['Website settings'],
                'summary' => 'Change a website\'s settings',
                'description' => 'Only the fields you send change. The website\'s type, PHP settings, redirects and SSL stay as they are (they have their own endpoints). Unknown fields are refused with `422`.',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'properties' => $props], ['aliases' => ['www.shop.example.com'], 'client_max_body_mb' => 128, 'monitoring' => false]),
                'responses' => ['200' => O::res('The settings, changed.', $ref, $settings), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/reapply' => ['post' => [
            'operationId' => 'reapplyWebsite',
            'tags' => ['Website settings'],
            'summary' => 'Re-apply a website\'s configuration',
            'description' => 'Writes the website\'s web server configuration again from the panel\'s settings, checks it and reloads. Use it after something changed the server behind the panel\'s back. Safe to repeat.',
            'x-permission' => 'websites.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('Re-applied.', ['type' => 'object', 'properties' => ['reapplied' => ['type' => 'string', 'description' => 'The domain.']]], ['reapplied' => 'shop.example.com']), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/permissions' => ['get' => [
            'operationId' => 'checkWebsitePermissions',
            'tags' => ['Website settings'],
            'summary' => 'Check the permissions of a website\'s files',
            'description' => 'A dry run: counts the files and folders whose owner or mode is not what the panel sets. Nothing is changed. A large website can take a while.',
            'x-permission' => 'websites.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('The check.', O::ref('SitePermissions'), $perm), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/permissions/repair' => ['post' => [
            'operationId' => 'repairWebsitePermissions',
            'tags' => ['Website settings'],
            'summary' => 'Repair the permissions of a website\'s files',
            'description' => 'Sets the owner and mode of the website\'s files and folders to what the panel expects. Links and special files are left as they are (`skipped`). Safe to repeat; recorded in the audit log.',
            'x-permission' => 'websites.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('The repair.', O::ref('SitePermissions'), ['repaired' => true, 'checked' => 120, 'wrong' => 3, 'fixed' => 3, 'skipped' => 1]), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/env' => [
            'get' => [
                'operationId' => 'getWebsiteEnv',
                'tags' => ['Website settings'],
                'summary' => 'List a website\'s environment variable names',
                'description' => 'The **names** only, sorted. Values are never returned by the API; read them in the panel if you must.',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The names.', O::ref('SiteEnv'), $env), '404' => O::err('NotFound')],
            ],
            'patch' => [
                'operationId' => 'updateWebsiteEnv',
                'tags' => ['Website settings'],
                'summary' => 'Set or remove environment variables',
                'description' => '`set` adds or replaces variables, `unset` removes them; every other variable stays. The result is checked as a whole (names such as `PATH` that the system owns are refused, a value may have at most 4096 characters, at most 100 variables) before anything is stored, and the website\'s workers are restarted to see the change. Values are stored encrypted and never appear in responses or the audit log.',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'properties' => [
                    'set' => ['type' => 'object', 'additionalProperties' => ['type' => 'string'], 'description' => 'Variables to add or replace, as `NAME: value`.'],
                    'unset' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'Names to remove.'],
                ]], ['set' => ['APP_ENV' => 'production'], 'unset' => ['OLD_KEY']]),
                'responses' => ['200' => O::res('The names after the change.', O::ref('SiteEnv'), $env + ['note' => 'Saved and applied.']), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/runtime' => [
            'get' => [
                'operationId' => 'getWebsiteRuntime',
                'tags' => ['Website settings'],
                'summary' => 'Get the runtime of a Node.js or Python website',
                'description' => 'The runtime in use and the installed versions that can be chosen. `409` for other website types.',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The runtime.', O::ref('SiteRuntime'), $runtime), '404' => O::err('NotFound'), '409' => O::err('Conflict')],
            ],
            'patch' => [
                'operationId' => 'updateWebsiteRuntime',
                'tags' => ['Website settings'],
                'summary' => 'Change the runtime version',
                'description' => 'Moves the website, and its `app` worker, to another installed version of the same runtime (`node:…` for Node.js, `python:…` for Python).',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'required' => ['app_runtime'], 'properties' => ['app_runtime' => ['type' => 'string', 'description' => 'One of `available`.', 'examples' => ['node:22.0.0']]]], ['app_runtime' => 'node:22.0.0']),
                'responses' => ['200' => O::res('The runtime, changed.', O::ref('SiteRuntime'), ['app_runtime' => 'node:22.0.0'] + $runtime), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed')],
            ],
        ],
    ],
];
