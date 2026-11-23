<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$website = [
    'id' => 7, 'domain' => 'shop.example.com', 'aliases' => ['www.shop.example.com'], 'kind' => 'php', 'php_version' => '8.3',
    'document_root' => '/home/shop/shop.example.com/public_html', 'system_user' => 'shop', 'https' => false, 'enabled' => true,
    'status' => 'active', 'owner_id' => 3, 'created_at' => '2026-11-23T19:40:00Z',
];
$id = ['$ref' => '#/components/parameters/Id'];
$server = ['$ref' => '#/components/parameters/Server'];

return [
    'tags' => [
        ['name' => 'Websites', 'description' => 'Create, inspect, enable, disable and delete websites. Changes are validated (`nginx -t`, `php-fpm -t`), applied and rolled back on failure, exactly as in the web interface.'],
        ['name' => 'SSL', 'description' => 'HTTPS certificates for a website.'],
        ['name' => 'Backups', 'description' => 'Website backups (files and databases), optionally encrypted.'],
        ['name' => 'Deployments', 'description' => 'Deploy a website from its connected Git repository.'],
    ],
    'schemas' => [
        'Website' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer'],
                'domain' => ['type' => 'string', 'description' => 'The primary host name.'],
                'aliases' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'Other host names served by the same website.'],
                'kind' => ['type' => 'string', 'enum' => ['php', 'static', 'proxy'], 'description' => '`php`: PHP-FPM; `static`: files only; `proxy`: reverse proxy to an application.'],
                'php_version' => ['type' => 'string', 'description' => 'For `php` websites, for example `8.3`.'],
                'document_root' => ['type' => 'string'],
                'system_user' => ['type' => 'string', 'description' => 'The Linux account the website runs as.'],
                'https' => ['type' => 'boolean', 'description' => 'True once a certificate is installed and HTTPS is on.'],
                'enabled' => ['type' => 'boolean'],
                'status' => ['type' => 'string', 'description' => 'For example `active`, `pending`, `disabled`.'],
                'owner_id' => ['type' => ['integer', 'null'], 'description' => 'The panel user who owns it.'],
                'created_at' => ['type' => 'string', 'format' => 'date-time'],
            ],
        ],
        'Backup' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer'],
                'backup_id' => ['type' => 'string', 'description' => 'The backup\'s identifier on the server.'],
                'label' => ['type' => 'string'],
                'status' => ['type' => 'string', 'enum' => ['running', 'complete', 'failed']],
                'size' => ['type' => 'integer', 'description' => 'Bytes.'],
                'encrypted' => ['type' => 'boolean'],
                'parts' => ['description' => 'What the backup contains (files, databases).'],
                'created_at' => ['type' => 'string', 'format' => 'date-time'],
                'error' => ['type' => ['string', 'null']],
            ],
        ],
    ],
    'paths' => [
        '/websites' => [
            'get' => [
                'operationId' => 'listWebsites',
                'tags' => ['Websites'],
                'summary' => 'List websites',
                'description' => 'The websites visible to the token (a hosting customer only sees their own).',
                'x-permission' => 'websites.view',
                'parameters' => [$server],
                'responses' => ['200' => O::res('The websites.', ['type' => 'array', 'items' => O::ref('Website')], [$website])],
            ],
            'post' => [
                'operationId' => 'createWebsite',
                'tags' => ['Websites'],
                'summary' => 'Create a website',
                'description' => 'Validated against the hosting plan\'s limits, written as an nginx site (and a PHP-FPM pool for `php`), checked with `nginx -t` / `php-fpm -t`, applied, and rolled back on failure.',
                'x-permission' => 'websites.manage',
                'parameters' => [$server],
                'requestBody' => O::body([
                    'type' => 'object',
                    'required' => ['domain'],
                    'properties' => [
                        'domain' => ['type' => 'string', 'description' => 'The primary host name.', 'examples' => ['shop.example.com']],
                        'aliases' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'Other host names.', 'examples' => [['www.shop.example.com']]],
                        'kind' => ['type' => 'string', 'enum' => ['php', 'static', 'proxy'], 'default' => 'php'],
                        'php_version' => ['type' => 'string', 'description' => 'One of the PHP versions installed on the server (for `php` websites).', 'examples' => ['8.3']],
                        'proxy_pass' => ['type' => 'string', 'description' => 'Required for `proxy`: where requests are forwarded.', 'examples' => ['http://127.0.0.1:3000']],
                        'system_user' => ['type' => 'string', 'description' => 'The Linux account to run as (administrators; defaults to a name derived from the domain).'],
                    ],
                ], ['domain' => 'shop.example.com', 'aliases' => ['www.shop.example.com'], 'kind' => 'php', 'php_version' => '8.3']),
                'responses' => [
                    '201' => O::res('The new website.', O::ref('Website'), $website),
                    '409' => O::err('Conflict'),
                    '422' => O::err('ValidationFailed'),
                    '502' => O::err('AgentError'),
                ],
            ],
        ],
        '/websites/{id}' => [
            'get' => [
                'operationId' => 'getWebsite',
                'tags' => ['Websites'],
                'summary' => 'Get a website',
                'x-permission' => 'websites.view',
                'parameters' => [$id, $server],
                'responses' => ['200' => O::res('The website.', O::ref('Website'), $website), '404' => O::err('NotFound')],
            ],
            'delete' => [
                'operationId' => 'deleteWebsite',
                'tags' => ['Websites'],
                'summary' => 'Delete a website',
                'description' => 'Removes the web server configuration and the PHP-FPM pool. **Files and the Linux account are kept** unless you ask for them to be removed (the account is kept anyway while another website uses it).',
                'x-permission' => 'websites.manage',
                'parameters' => [
                    $id, $server,
                    O::query('remove_files', 'Also delete the website\'s files.', ['type' => 'string', 'enum' => ['0', '1'], 'default' => '0']),
                    O::query('remove_user', 'Also delete the Linux account (when no other website uses it).', ['type' => 'string', 'enum' => ['0', '1'], 'default' => '0']),
                ],
                'responses' => [
                    '200' => O::res('Deleted; `warnings` lists what could not be cleaned up.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'integer'], 'warnings' => ['type' => 'array', 'items' => ['type' => 'string']]]], ['deleted' => 7, 'warnings' => []]),
                    '404' => O::err('NotFound'),
                    '409' => O::err('Conflict'),
                    '502' => O::err('AgentError'),
                ],
            ],
        ],
        '/websites/{id}/enable' => ['post' => [
            'operationId' => 'enableWebsite',
            'tags' => ['Websites'],
            'summary' => 'Enable a website',
            'description' => 'Serves the website again after it was disabled.',
            'x-permission' => 'websites.manage',
            'parameters' => [$id, $server],
            'responses' => ['200' => O::res('The website, enabled.', O::ref('Website'), ['enabled' => true, 'status' => 'active'] + $website), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/disable' => ['post' => [
            'operationId' => 'disableWebsite',
            'tags' => ['Websites'],
            'summary' => 'Disable a website',
            'description' => 'Stops serving the website; its files and data stay.',
            'x-permission' => 'websites.manage',
            'parameters' => [$id, $server],
            'responses' => ['200' => O::res('The website, disabled.', O::ref('Website'), ['enabled' => false, 'status' => 'disabled'] + $website), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/ssl' => ['post' => [
            'operationId' => 'issueWebsiteSsl',
            'tags' => ['SSL'],
            'summary' => 'Issue a Let\'s Encrypt certificate and turn HTTPS on',
            'description' => 'The domain (and aliases) must resolve to this server. The certificate is issued, installed, the web server configuration is checked and reloaded, and HTTP redirects to HTTPS. Renewal is automatic.',
            'x-permission' => 'ssl.manage',
            'parameters' => [$id, $server],
            'requestBody' => O::body([
                'type' => 'object',
                'properties' => [
                    'email' => ['type' => 'string', 'format' => 'email', 'description' => 'Contact address for expiry notices.'],
                    'include_aliases' => ['type' => 'boolean', 'default' => true, 'description' => 'Cover the aliases in the same certificate.'],
                    'staging' => ['type' => 'boolean', 'default' => false, 'description' => 'Use Let\'s Encrypt\'s test environment (the certificate is not trusted).'],
                ],
            ], ['email' => 'admin@example.com', 'include_aliases' => true], false),
            'responses' => [
                '200' => O::res('The website, with `https` true.', O::ref('Website'), ['https' => true] + $website),
                '404' => O::err('NotFound'),
                '409' => O::err('Conflict'),
                '422' => O::err('ValidationFailed'),
                '502' => O::err('AgentError'),
            ],
        ]],
        '/websites/{id}/backups' => [
            'get' => [
                'operationId' => 'listWebsiteBackups',
                'tags' => ['Backups'],
                'summary' => 'List a website\'s backups',
                'x-permission' => 'backups.view',
                'parameters' => [$id, $server],
                'responses' => [
                    '200' => O::res('The backups, newest first.', ['type' => 'array', 'items' => O::ref('Backup')], [[
                        'id' => 12, 'backup_id' => '20261123T200100Z-3f9a1c', 'label' => 'before upgrade', 'status' => 'complete', 'size' => 73400320,
                        'encrypted' => true, 'parts' => ['files', 'databases'], 'created_at' => '2026-11-23T20:01:00Z', 'error' => null,
                    ]]),
                    '404' => O::err('NotFound'),
                    '502' => O::err('AgentError'),
                ],
            ],
            'post' => [
                'operationId' => 'createWebsiteBackup',
                'tags' => ['Backups'],
                'summary' => 'Start a backup',
                'description' => 'Runs in the background: the response is `202` with a `job_id`; follow it with `GET /jobs/{id}`.',
                'x-permission' => 'backups.manage',
                'parameters' => [$id, $server],
                'requestBody' => O::body([
                    'type' => 'object',
                    'properties' => [
                        'files' => ['type' => 'boolean', 'default' => true, 'description' => 'Include the website\'s files.'],
                        'databases' => ['type' => 'boolean', 'default' => true, 'description' => 'Include its databases.'],
                        'encrypt' => ['type' => 'boolean', 'default' => true, 'description' => 'Encrypt the archive.'],
                        'label' => ['type' => 'string', 'description' => 'A note shown in the backup list.'],
                    ],
                ], ['files' => true, 'databases' => true, 'label' => 'before upgrade'], false),
                'responses' => [
                    '202' => O::res('The backup started.', O::ref('Job'), ['job_id' => '3f9a1c2b7d4e5a60']),
                    '404' => O::err('NotFound'),
                    '409' => O::err('Conflict'),
                    '502' => O::err('AgentError'),
                ],
            ],
        ],
        '/websites/{id}/deploy' => ['post' => [
            'operationId' => 'deployWebsite',
            'tags' => ['Deployments'],
            'summary' => 'Deploy from the connected Git repository',
            'description' => 'Pulls the repository connected under the website\'s Deploy tab and runs its steps, in the background. `202` with a `job_id`.',
            'x-permission' => 'deploy.manage',
            'parameters' => [$id, $server],
            'responses' => [
                '202' => O::res('The deployment started.', O::ref('Job'), ['job_id' => '9b1d4e5a603f9a1c']),
                '404' => O::err('NotFound'),
                '409' => O::err('Conflict'),
                '502' => O::err('AgentError'),
            ],
        ]],
    ],
];
