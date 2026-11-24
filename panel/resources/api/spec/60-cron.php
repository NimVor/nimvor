<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$job = ['id' => 14, 'schedule' => '*/15 * * * *', 'command' => 'php artisan schedule:run', 'description' => 'Laravel scheduler', 'enabled' => true, 'email_output' => false, 'created_at' => '2026-11-23T20:00:00Z', 'updated_at' => '2026-11-23T20:00:00Z'];
$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The website\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$jb = ['name' => 'job', 'in' => 'path', 'required' => true, 'description' => 'The cron job\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$ref = O::ref('CronJob');
$props = [
    'schedule' => ['type' => 'string', 'description' => 'Five fields (minute hour day month weekday), for example `*/5 * * * *`, or `@hourly`, `@daily`, `@weekly`, `@monthly`. Names such as `mon` or `jan` are not supported.', 'examples' => ['*/15 * * * *']],
    'command' => ['type' => 'string', 'maxLength' => 2000, 'description' => 'One line. It runs as the website\'s system account, from its home folder.', 'examples' => ['php artisan schedule:run']],
    'description' => ['type' => 'string', 'maxLength' => 200],
    'enabled' => ['type' => 'boolean', 'default' => true, 'description' => 'False pauses the job; it stays in the list.'],
    'email_output' => ['type' => 'boolean', 'default' => false, 'description' => 'Mail the job\'s output to the token\'s user.'],
];

return [
    'tags' => [
        ['name' => 'Cron jobs', 'description' => "Scheduled commands of a website, run as the website's system account.\n\nA change rewrites the server's cron file for the website, which is checked; if the server refuses it the change is undone. A website can have at most 200 jobs and the plan's cron limit applies. A suspended account's jobs do not run (their settings are kept)."],
    ],
    'schemas' => [
        'CronJob' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer'],
                'schedule' => $props['schedule'],
                'command' => ['type' => 'string'],
                'description' => ['type' => 'string'],
                'enabled' => ['type' => 'boolean'],
                'email_output' => ['type' => 'boolean', 'description' => 'The output is mailed to the person who saved the job.'],
                'created_at' => ['type' => 'string', 'format' => 'date-time'],
                'updated_at' => ['type' => 'string', 'format' => 'date-time'],
            ],
        ],
    ],
    'paths' => [
        '/websites/{id}/cron' => [
            'get' => [
                'operationId' => 'listCronJobs',
                'tags' => ['Cron jobs'],
                'summary' => 'List a website\'s cron jobs',
                'x-permission' => 'cron.view',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The jobs.', ['type' => 'array', 'items' => $ref], [$job]), '404' => O::err('NotFound')],
            ],
            'post' => [
                'operationId' => 'createCronJob',
                'tags' => ['Cron jobs'],
                'summary' => 'Add a cron job',
                'x-permission' => 'cron.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'required' => ['schedule', 'command'], 'properties' => $props], ['schedule' => '*/15 * * * *', 'command' => 'php artisan schedule:run', 'description' => 'Laravel scheduler']),
                'responses' => ['201' => O::res('The job.', $ref, $job), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/cron/{job}' => [
            'get' => [
                'operationId' => 'getCronJob',
                'tags' => ['Cron jobs'],
                'summary' => 'Get a cron job',
                'x-permission' => 'cron.view',
                'parameters' => [$id, $jb],
                'responses' => ['200' => O::res('The job.', $ref, $job), '404' => O::err('NotFound')],
            ],
            'patch' => [
                'operationId' => 'updateCronJob',
                'tags' => ['Cron jobs'],
                'summary' => 'Change a cron job',
                'description' => 'Only the fields you send change; `{"enabled": false}` pauses the job.',
                'x-permission' => 'cron.manage',
                'parameters' => [$id, $jb],
                'requestBody' => O::body(['type' => 'object', 'properties' => $props], ['enabled' => false]),
                'responses' => ['200' => O::res('The job, changed.', $ref, ['enabled' => false] + $job), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
            'delete' => [
                'operationId' => 'deleteCronJob',
                'tags' => ['Cron jobs'],
                'summary' => 'Delete a cron job',
                'x-permission' => 'cron.manage',
                'parameters' => [$id, $jb],
                'responses' => ['200' => O::res('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'integer']]], ['deleted' => 14]), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/cron/{job}/run' => ['post' => [
            'operationId' => 'runCronJob',
            'tags' => ['Cron jobs'],
            'summary' => 'Run a cron job now',
            'description' => 'Starts the command once, in the background, as the website\'s system account. `202` with a `job_id`: follow it with `GET /jobs/{id}`, or read the output with `GET /websites/{id}/cron/{job}/runs`. Not available for a suspended account (`409`).',
            'x-permission' => 'cron.manage',
            'parameters' => [$id, $jb],
            'responses' => ['202' => O::res('The run started.', O::ref('Job'), ['job_id' => '3f9a1c2b7d4e5a60']), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/cron/{job}/runs' => ['get' => [
            'operationId' => 'listCronRuns',
            'tags' => ['Cron jobs'],
            'summary' => 'The job\'s recent runs',
            'description' => 'Read from the job\'s log: when each run started and ended, its exit code and its output, newest first.',
            'x-permission' => 'cron.view',
            'parameters' => [$id, $jb],
            'responses' => ['200' => O::res('The runs.', ['type' => 'object', 'properties' => [
                'runs' => ['type' => 'array', 'items' => ['type' => 'object', 'properties' => [
                    'start' => ['type' => ['string', 'null']], 'end' => ['type' => ['string', 'null']], 'exit' => ['type' => ['integer', 'null'], 'description' => 'The exit code; 0 is success.'],
                    'manual' => ['type' => 'boolean', 'description' => 'Started with `POST …/run`.'], 'output' => ['type' => 'string'],
                ]]],
                'truncated' => ['type' => 'boolean', 'description' => 'Older runs were cut off.'],
                'size' => ['type' => 'integer', 'description' => 'Log size in bytes.'],
            ]], ['runs' => [['start' => '2026-11-23T20:15:00Z', 'end' => '2026-11-23T20:15:02Z', 'exit' => 0, 'manual' => false, 'output' => "Running scheduled tasks\n"]], 'truncated' => false, 'size' => 120]), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
        ]],
    ],
];
