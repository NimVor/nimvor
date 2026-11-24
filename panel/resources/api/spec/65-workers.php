<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$worker = ['id' => 5, 'name' => 'Queue worker', 'unit' => 'nimvor-worker-shop-queue-worker.service', 'command' => 'php artisan queue:work --sleep=3', 'directory' => '', 'runtime' => '', 'restart' => 'always', 'procs' => 2, 'memory_mb' => 512, 'enabled' => true, 'env_keys' => ['QUEUE', 'APP_ENV'], 'state' => ['active' => 'active', 'sub' => 'running', 'running' => true, 'procs_running' => 2, 'memory_bytes' => 96468992, 'active_since' => '2026-11-24T08:10:00Z'], 'created_at' => '2026-11-24T08:00:00Z', 'updated_at' => '2026-11-24T08:00:00Z'];
$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The website\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$wk = ['name' => 'worker', 'in' => 'path', 'required' => true, 'description' => 'The worker\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$ref = O::ref('Worker');
$props = [
    'name' => ['type' => 'string', 'maxLength' => 100, 'examples' => ['Queue worker']],
    'command' => ['type' => 'string', 'maxLength' => 2000, 'description' => 'One line, run from `directory` as the website\'s system account.', 'examples' => ['php artisan queue:work --sleep=3']],
    'directory' => ['type' => 'string', 'maxLength' => 200, 'description' => 'A folder inside the website the command runs from; empty means the website\'s folder.'],
    'runtime' => ['type' => 'string', 'description' => '`node:<version>` or `python:<version>` of an installed runtime; empty or `system` uses the server\'s own.', 'examples' => ['node:22']],
    'env' => ['type' => 'object', 'additionalProperties' => ['type' => 'string'], 'description' => 'Variables for this worker, names in capitals (up to 50). They are added to the website\'s own variables; the worker\'s win. **Replaces the worker\'s whole set.** The values are never returned.', 'examples' => [['QUEUE' => 'default']]],
    'restart' => ['type' => 'string', 'enum' => ['always', 'on-failure', 'no'], 'default' => 'always'],
    'procs' => ['type' => 'integer', 'minimum' => 1, 'maximum' => 20, 'default' => 1, 'description' => 'How many identical copies run.'],
    'memory_mb' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 65536, 'default' => 0, 'description' => 'Memory limit per copy; 0 means none.'],
    'enabled' => ['type' => 'boolean', 'default' => true, 'description' => 'False stops the worker; it stays in the list. A suspended account\'s workers stay stopped.'],
];

return [
    'tags' => [
        ['name' => 'Workers', 'description' => "Background workers of a website: queue workers, Node.js and Python apps and other long-running processes, run by systemd as the website's system account (`nimvor-worker-<name>.service`) and restarted by the rules you set.\n\nA change is written and started by the server; if it refuses it the change is undone. The plan's worker limit applies. The values of a worker's environment variables are never returned (their names are), because they often hold secrets."],
    ],
    'schemas' => [
        'Worker' => [
            'type' => 'object',
            'properties' => ['id' => ['type' => 'integer'], 'unit' => ['type' => 'string', 'description' => 'The systemd unit.'], 'env_keys' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'Names of the variables set (not their values).'],
                'state' => ['type' => ['object', 'null'], 'description' => 'What systemd reports now; null when the server cannot say.', 'properties' => [
                    'active' => ['type' => 'string', 'description' => 'For example `active` or `inactive`.'], 'sub' => ['type' => 'string'], 'running' => ['type' => 'boolean', 'description' => 'At least one copy is up.'],
                    'procs_running' => ['type' => 'integer'], 'memory_bytes' => ['type' => ['integer', 'null']], 'active_since' => ['type' => ['string', 'null'], 'format' => 'date-time'],
                ]],
                'created_at' => ['type' => 'string', 'format' => 'date-time'], 'updated_at' => ['type' => 'string', 'format' => 'date-time'],
            ] + array_diff_key($props, ['env' => 1]),
        ],
    ],
    'paths' => [
        '/websites/{id}/workers' => [
            'get' => [
                'operationId' => 'listWorkers',
                'tags' => ['Workers'],
                'summary' => 'List a website\'s workers',
                'description' => 'With what systemd reports for each.',
                'x-permission' => 'workers.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The workers.', ['type' => 'array', 'items' => $ref], [$worker]), '404' => O::err('NotFound')],
            ],
            'post' => [
                'operationId' => 'createWorker',
                'tags' => ['Workers'],
                'summary' => 'Create a worker',
                'description' => 'Writes the unit, and starts it unless `enabled` is false.',
                'x-permission' => 'workers.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'required' => ['name', 'command'], 'properties' => $props], ['name' => 'Queue worker', 'command' => 'php artisan queue:work --sleep=3', 'procs' => 2, 'memory_mb' => 512, 'env' => ['QUEUE' => 'default']]),
                'responses' => ['201' => O::res('The worker.', $ref, $worker), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/workers/{worker}' => [
            'get' => [
                'operationId' => 'getWorker',
                'tags' => ['Workers'],
                'summary' => 'Get a worker',
                'x-permission' => 'workers.manage',
                'parameters' => [$id, $wk],
                'responses' => ['200' => O::res('The worker.', $ref, $worker), '404' => O::err('NotFound')],
            ],
            'patch' => [
                'operationId' => 'updateWorker',
                'tags' => ['Workers'],
                'summary' => 'Change a worker',
                'description' => 'Only the fields you send change (`env` replaces the whole set). The worker is rewritten and restarted. `{"enabled": false}` stops it.',
                'x-permission' => 'workers.manage',
                'parameters' => [$id, $wk],
                'requestBody' => O::body(['type' => 'object', 'properties' => $props], ['procs' => 4]),
                'responses' => ['200' => O::res('The worker, changed.', $ref, ['procs' => 4] + $worker), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
            'delete' => [
                'operationId' => 'deleteWorker',
                'tags' => ['Workers'],
                'summary' => 'Delete a worker',
                'description' => 'Stops it and removes its unit.',
                'x-permission' => 'workers.manage',
                'parameters' => [$id, $wk],
                'responses' => ['200' => O::res('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'integer']]], ['deleted' => 5]), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/workers/{worker}/restart' => ['post' => [
            'operationId' => 'restartWorker',
            'tags' => ['Workers'],
            'summary' => 'Restart a worker',
            'description' => 'Restarts every copy (for example after a deploy). Not available for a suspended account (`409`).',
            'x-permission' => 'workers.manage',
            'parameters' => [$id, $wk],
            'responses' => ['200' => O::res('The worker.', $ref, $worker), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
    ],
];
