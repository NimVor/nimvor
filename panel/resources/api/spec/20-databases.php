<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

return [
    'tags' => [
        ['name' => 'Databases', 'description' => 'MySQL and MariaDB databases and their users.'],
    ],
    'schemas' => [
        'Database' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer'],
                'name' => ['type' => 'string'],
                'website_id' => ['type' => ['integer', 'null'], 'description' => 'The website it belongs to, if linked.'],
                'created_at' => ['type' => 'string', 'format' => 'date-time'],
            ],
        ],
    ],
    'paths' => [
        '/databases' => [
            'get' => [
                'operationId' => 'listDatabases',
                'tags' => ['Databases'],
                'summary' => 'List databases',
                'x-permission' => 'databases.view',
                'parameters' => [['$ref' => '#/components/parameters/Server']],
                'responses' => ['200' => O::res('The databases visible to the token.', ['type' => 'array', 'items' => O::ref('Database')], [['id' => 4, 'name' => 'shop_wp', 'website_id' => 7, 'created_at' => '2026-11-23T19:45:00Z']])],
            ],
            'post' => [
                'operationId' => 'createDatabase',
                'tags' => ['Databases'],
                'summary' => 'Create a database, optionally with a user',
                'description' => 'Hosting customers\' names get their account prefix. When `create_user` is true a user with all privileges on this database is created and **its generated password is returned once**, in this response only (unless `store_password` keeps it encrypted in the panel).',
                'x-permission' => 'databases.manage',
                'parameters' => [['$ref' => '#/components/parameters/Server']],
                'requestBody' => O::body([
                    'type' => 'object',
                    'required' => ['name'],
                    'properties' => [
                        'name' => ['type' => 'string', 'pattern' => '^[a-z0-9_]{1,64}$', 'description' => 'Lowercase letters, digits and underscores.', 'examples' => ['shop_wp']],
                        'website_id' => ['type' => 'integer', 'description' => 'Link the database to one of the token\'s websites.'],
                        'create_user' => ['type' => 'boolean', 'default' => false, 'description' => 'Also create a user with all privileges on it.'],
                        'username' => ['type' => 'string', 'pattern' => '^[a-z0-9_]{1,32}$', 'description' => 'The user\'s name (default: derived from the database name).'],
                        'store_password' => ['type' => 'boolean', 'default' => false, 'description' => 'Keep the generated password, encrypted, so it can be shown in the panel later.'],
                    ],
                ], ['name' => 'shop_wp', 'website_id' => 7, 'create_user' => true]),
                'responses' => [
                    '201' => O::res('Created. `user` and `password` are present only when a user was created.', [
                        'type' => 'object',
                        'properties' => [
                            'database_id' => ['type' => 'integer'],
                            'user' => ['type' => 'string'],
                            'password' => ['type' => 'string', 'description' => 'Shown once.'],
                        ],
                    ], ['database_id' => 4, 'user' => 'shop_wp', 'password' => 'Qm7vR2x9…']),
                    '409' => O::err('Conflict'),
                    '422' => O::err('ValidationFailed'),
                    '502' => O::err('AgentError'),
                ],
            ],
        ],
    ],
];
