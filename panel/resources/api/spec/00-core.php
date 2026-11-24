<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

return [
    'tags' => [
        ['name' => 'Account', 'description' => 'Who the token belongs to and which server it acts on.'],
        ['name' => 'Server', 'description' => 'System information and current metrics of a managed server.'],
        ['name' => 'Jobs', 'description' => 'Background jobs. Long operations (backups, deployments) return a `job_id`; follow it here.'],
        ['name' => 'Notifications', 'description' => 'Job results and alerts the token\'s user may see.'],
    ],
    'schemas' => [
        'Job' => [
            'type' => 'object',
            'required' => ['job_id'],
            'description' => 'A background job that was started.',
            'properties' => ['job_id' => ['type' => 'string', 'pattern' => '^[a-f0-9]{16}$', 'description' => 'Pass it to `GET /jobs/{id}`.']],
        ],
        'JobStatus' => [
            'type' => 'object',
            'properties' => [
                'job' => ['type' => 'object', 'properties' => [
                    'id' => ['type' => 'string'],
                    'kind' => ['type' => 'string', 'description' => 'What the job does, for example `backup.site`.'],
                    'target' => ['type' => 'string'],
                    'status' => ['type' => 'string', 'enum' => ['queued', 'running', 'succeeded', 'failed']],
                    'error' => ['type' => 'string', 'description' => 'Why it failed (only when `status` is `failed`).'],
                    'result' => ['description' => 'What the job produced, if anything.'],
                    'created_at' => ['type' => 'string', 'format' => 'date-time'],
                    'started_at' => ['type' => 'string', 'format' => 'date-time'],
                    'finished_at' => ['type' => 'string', 'format' => 'date-time'],
                ]],
                'log' => ['type' => 'string', 'description' => 'Log output since `offset` (at most 256 KiB per call).'],
                'offset' => ['type' => 'integer', 'description' => 'Pass as `offset` on the next call to get only newer output.'],
                'done' => ['type' => 'boolean', 'description' => 'True once the job has finished (succeeded or failed).'],
                'kind' => ['type' => 'string'],
            ],
        ],
        'Notification' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer'],
                'server_id' => ['type' => 'integer'],
                'website_id' => ['type' => ['integer', 'null']],
                'domain' => ['type' => ['string', 'null']],
                'kind' => ['type' => 'string', 'description' => 'For example `job`, `alert`.'],
                'severity' => ['type' => 'string', 'enum' => ['info', 'success', 'warning', 'error']],
                'title' => ['type' => 'string'],
                'body' => ['type' => 'string'],
                'job_id' => ['type' => ['string', 'null']],
                'read' => ['type' => 'boolean'],
                'created_at' => ['type' => 'string', 'format' => 'date-time'],
            ],
        ],
    ],
    'paths' => [
        '/me' => ['get' => [
            'operationId' => 'getMe',
            'tags' => ['Account'],
            'summary' => 'The token\'s user and the selected server',
            'description' => 'Use it to check a token. It needs no permission beyond a valid token.',
            'responses' => [
                '200' => O::res('The user, the token and the server it acts on.', [
                    'type' => 'object',
                    'properties' => [
                        'id' => ['type' => 'integer'],
                        'username' => ['type' => 'string'],
                        'role' => ['type' => 'string', 'description' => 'The role name, for example `admin` or `customer`.'],
                        'scope' => ['type' => 'string', 'enum' => ['all', 'own'], 'description' => '`own`: only the user\'s own resources are visible.'],
                        'token' => ['type' => 'object', 'properties' => [
                            'name' => ['type' => 'string'],
                            'ability' => ['type' => 'string', 'enum' => ['read', 'write']],
                            'expires_at' => ['type' => ['string', 'null'], 'format' => 'date-time'],
                        ]],
                        'server_id' => ['type' => 'integer'],
                    ],
                ], ['id' => 1, 'username' => 'admin', 'role' => 'admin', 'scope' => 'all', 'token' => ['name' => 'deploy script', 'ability' => 'write', 'expires_at' => '2027-02-21T09:00:00Z'], 'server_id' => 1]),
            ],
        ]],
        '/server' => ['get' => [
            'operationId' => 'getServer',
            'tags' => ['Server'],
            'summary' => 'System information and current metrics',
            'description' => 'Operating system, kernel, CPU, uptime and network interfaces, with the latest CPU, memory, disk, load and network sample.',
            'x-permission' => 'server.view',
            'parameters' => [['$ref' => '#/components/parameters/Server']],
            'responses' => [
                '200' => O::res('Information and the latest sample.', [
                    'type' => 'object',
                    'properties' => [
                        'info' => ['type' => 'object', 'properties' => [
                            'agent_version' => ['type' => 'string'],
                            'os' => ['type' => 'object', 'description' => 'Distribution name and version.'],
                            'system' => ['type' => 'object', 'properties' => [
                                'hostname' => ['type' => 'string'], 'kernel' => ['type' => 'string'], 'architecture' => ['type' => 'string'],
                                'cpu' => ['type' => 'object', 'properties' => ['model' => ['type' => 'string'], 'cores' => ['type' => 'integer']]],
                                'uptime_seconds' => ['type' => 'number'], 'boot_time' => ['type' => 'string', 'format' => 'date-time'], 'timezone' => ['type' => 'string'],
                                'interfaces' => ['type' => 'array', 'items' => ['type' => 'object', 'properties' => ['name' => ['type' => 'string'], 'mac' => ['type' => 'string'], 'up' => ['type' => 'boolean'], 'addresses' => ['type' => 'array', 'items' => ['type' => 'string']]]]],
                            ]],
                        ]],
                        'metrics' => ['type' => 'object', 'properties' => [
                            'timestamp' => ['type' => 'string', 'format' => 'date-time'],
                            'cpu_percent' => ['type' => 'number'],
                            'memory' => ['type' => 'object', 'properties' => [
                                'total_bytes' => ['type' => 'integer'], 'available_bytes' => ['type' => 'integer'], 'used_bytes' => ['type' => 'integer'], 'used_percent' => ['type' => 'number'],
                                'swap_total_bytes' => ['type' => 'integer'], 'swap_used_bytes' => ['type' => 'integer'],
                            ]],
                            'load' => ['type' => 'object', 'properties' => ['load1' => ['type' => 'number'], 'load5' => ['type' => 'number'], 'load15' => ['type' => 'number'], 'procs_running' => ['type' => 'integer'], 'procs_total' => ['type' => 'integer']]],
                            'uptime_seconds' => ['type' => 'number'],
                            'disks' => ['type' => 'array', 'items' => ['type' => 'object', 'properties' => ['mount' => ['type' => 'string'], 'total_bytes' => ['type' => 'integer'], 'used_bytes' => ['type' => 'integer'], 'free_bytes' => ['type' => 'integer'], 'used_percent' => ['type' => 'number']]]],
                            'network' => ['type' => 'array', 'items' => ['type' => 'object', 'properties' => ['rx_bytes_per_sec' => ['type' => 'number'], 'tx_bytes_per_sec' => ['type' => 'number']]]],
                        ]],
                    ],
                ], [
                    'info' => ['agent_version' => '1.10.1', 'os' => ['name' => 'Ubuntu', 'version' => '24.04'], 'system' => ['hostname' => 'ns1', 'kernel' => '6.8.0-60-generic', 'architecture' => 'amd64', 'cpu' => ['model' => 'AMD EPYC 7282', 'cores' => 4], 'uptime_seconds' => 864000.5, 'timezone' => 'UTC']],
                    'metrics' => ['timestamp' => '2026-11-23T20:00:00Z', 'cpu_percent' => 7.4, 'memory' => ['total_bytes' => 8217247744, 'used_bytes' => 2147483648, 'used_percent' => 26.1], 'load' => ['load1' => 0.12, 'load5' => 0.2, 'load15' => 0.18], 'disks' => [['mount' => '/', 'total_bytes' => 214748364800, 'used_bytes' => 64424509440, 'free_bytes' => 150323855360, 'used_percent' => 30.0]]],
                ]),
                '502' => O::err('AgentError'),
            ],
        ]],
        '/jobs/{id}' => ['get' => [
            'operationId' => 'getJob',
            'tags' => ['Jobs'],
            'summary' => 'Status and log of a background job',
            'description' => 'Only jobs started by the token\'s user are visible. Poll until `done` is true, passing the returned `offset` to receive only new log lines.',
            'parameters' => [
                ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The `job_id` returned when the job was started.', 'schema' => ['type' => 'string', 'pattern' => '^[a-f0-9]{16}$']],
                O::query('offset', 'Byte offset in the job log to read from.', ['type' => 'integer', 'minimum' => 0, 'default' => 0]),
            ],
            'responses' => [
                '200' => O::res('The job and its log since `offset`.', O::ref('JobStatus'), [
                    'job' => ['id' => '3f9a1c2b7d4e5a60', 'kind' => 'backup.site', 'target' => 'example.com', 'status' => 'running', 'created_at' => '2026-11-23T20:01:00Z', 'started_at' => '2026-11-23T20:01:01Z'],
                    'log' => "Archiving website files of example.com…\n", 'offset' => 41, 'done' => false, 'kind' => 'backup.site',
                ]),
                '404' => O::err('NotFound'),
                '502' => O::err('AgentError'),
            ],
        ]],
        '/notifications' => ['get' => [
            'operationId' => 'listNotifications',
            'tags' => ['Notifications'],
            'summary' => 'Notifications, newest first',
            'x-permission' => 'notifications.view',
            'parameters' => [
                O::query('unread', 'Only unread notifications.', ['type' => 'string', 'enum' => ['1']]),
                O::query('limit', 'How many to return.', ['type' => 'integer', 'minimum' => 1, 'maximum' => 200, 'default' => 50]),
            ],
            'responses' => [
                '200' => O::res('The notifications.', ['type' => 'array', 'items' => O::ref('Notification')], [[
                    'id' => 41, 'server_id' => 1, 'website_id' => 7, 'domain' => 'example.com', 'kind' => 'job', 'severity' => 'success',
                    'title' => 'Backup example.com finished', 'body' => '', 'job_id' => '3f9a1c2b7d4e5a60', 'read' => false, 'created_at' => '2026-11-23T20:03:10Z',
                ]]),
            ],
        ]],
        '/notifications/read' => ['post' => [
            'operationId' => 'markNotificationsRead',
            'tags' => ['Notifications'],
            'summary' => 'Mark notifications as read',
            'description' => 'Give `ids` or `all: true`. Needs a read/write token.',
            'x-permission' => 'notifications.view',
            'requestBody' => O::body([
                'type' => 'object',
                'properties' => [
                    'ids' => ['type' => 'array', 'items' => ['type' => 'integer'], 'description' => 'The notification ids to mark.'],
                    'all' => ['type' => 'boolean', 'description' => 'Mark every notification of this user.'],
                ],
            ], ['ids' => [41, 42]], false),
            'responses' => [
                '200' => O::res('How many were marked and how many remain unread.', [
                    'type' => 'object',
                    'properties' => ['marked' => ['type' => 'integer'], 'unread' => ['type' => 'integer']],
                ], ['marked' => 2, 'unread' => 5]),
            ],
        ]],
    ],
];
