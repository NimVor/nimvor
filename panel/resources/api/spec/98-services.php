<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$unit = ['unit' => 'nginx.service', 'description' => 'A high performance web server and a reverse proxy server', 'active_state' => 'active', 'sub_state' => 'running', 'enabled' => 'enabled'];
$ref = O::ref('ServiceUnit');

return [
    'tags' => [
        ['name' => 'Services', 'description' => "The server's services (web server, PHP-FPM, databases, mail, DNS, FTP, …) and NimVor's own.\n\nNeeds the services permissions, which hosting customers do not have. The agent controls only the services on its allowlist and never SSH; it refuses anything else (`502` with a `reason`). Every operation is audited.\n\n**Restarting the web server or NimVor itself.** Nginx carries the panel and the agent and the panel restart a few seconds after they answer, so such a call answers `202` with `scheduled: true`, and the API may not answer for a moment afterwards."],
    ],
    'schemas' => [
        'ServiceUnit' => [
            'type' => 'object',
            'properties' => [
                'unit' => ['type' => 'string', 'description' => 'The systemd unit, for example `nginx.service`.'],
                'description' => ['type' => 'string'],
                'active_state' => ['type' => 'string', 'enum' => ['active', 'inactive', 'failed', 'activating', 'deactivating'], 'description' => 'Whether it is running now.'],
                'sub_state' => ['type' => 'string', 'description' => 'For example `running`, `exited` or `dead`.'],
                'enabled' => ['type' => 'string', 'description' => 'Whether it starts at boot: `enabled`, `disabled`, `static`, ….'],
            ],
        ],
    ],
    'paths' => [
        '/services' => ['get' => [
            'operationId' => 'listServices',
            'tags' => ['Services'],
            'summary' => 'List the services',
            'description' => 'The services NimVor manages on this server and their state.',
            'x-permission' => 'services.view',
            'responses' => ['200' => O::res('The services.', ['type' => 'array', 'items' => $ref], [$unit]), '403' => O::err('Forbidden'), '502' => O::err('AgentError')],
        ]],
        '/services/nimvor' => ['get' => [
            'operationId' => 'listNimvorServices',
            'tags' => ['Services'],
            'summary' => 'NimVor\'s own services',
            'description' => 'The state of the agent, the panel, the scheduler, greylisting, DKIM signing and the FTP service.',
            'x-permission' => 'services.view',
            'responses' => ['200' => O::res('The services.', ['type' => 'object'], ['services' => [['key' => 'agent', 'unit' => 'nimvor-agent.service', 'active_state' => 'active']]]), '403' => O::err('Forbidden'), '502' => O::err('AgentError')],
        ]],
        '/services/control' => ['post' => [
            'operationId' => 'controlService',
            'tags' => ['Services'],
            'summary' => 'Start, stop, restart, reload, enable or disable a service',
            'description' => '`enable` and `disable` change whether it starts at boot; `start`, `stop`, `restart` and `reload` act now. Prefer `reload` where a service supports it: it keeps running connections. Stopping a service stops what depends on it (a website\'s PHP, mail, …).',
            'x-permission' => 'services.manage',
            'requestBody' => O::body(['type' => 'object', 'required' => ['unit', 'op'], 'properties' => [
                'unit' => ['type' => 'string', 'pattern' => '^[A-Za-z0-9_][A-Za-z0-9@._:-]{0,199}\\.service$', 'description' => 'The unit, as `GET /services` lists it.', 'examples' => ['php8.3-fpm.service']],
                'op' => ['type' => 'string', 'enum' => ['start', 'stop', 'restart', 'reload', 'enable', 'disable']],
            ]], ['unit' => 'php8.3-fpm.service', 'op' => 'reload']),
            'responses' => [
                '200' => O::res('Done.', ['type' => 'object', 'properties' => ['unit' => ['type' => 'string'], 'op' => ['type' => 'string'], 'scheduled' => ['type' => 'boolean'], 'message' => ['type' => 'string']]], ['unit' => 'php8.3-fpm.service', 'op' => 'reload', 'scheduled' => false, 'message' => 'Reload of php8.3-fpm.service completed.']),
                '202' => O::res('The restart is scheduled for a few seconds from now (the web server carries the panel).', ['type' => 'object', 'properties' => ['unit' => ['type' => 'string'], 'op' => ['type' => 'string'], 'scheduled' => ['type' => 'boolean'], 'message' => ['type' => 'string']]], ['unit' => 'nginx.service', 'op' => 'restart', 'scheduled' => true, 'message' => 'nginx.service restarts in a few seconds; the panel may not answer for a moment.']),
                '403' => O::err('Forbidden'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError'),
            ],
        ]],
        '/services/nimvor/{key}/restart' => ['post' => [
            'operationId' => 'restartNimvorService',
            'tags' => ['Services'],
            'summary' => 'Restart one of NimVor\'s own services',
            'description' => 'Administrators only. The agent and the panel restart two seconds after answering (running tasks finish first), so expect `202` and a short gap.',
            'x-permission' => 'services.manage',
            'parameters' => [['name' => 'key', 'in' => 'path', 'required' => true, 'description' => 'Which service.', 'schema' => ['type' => 'string', 'enum' => ['agent', 'panel', 'scheduler', 'greylist', 'dkim', 'ftpd']]]],
            'responses' => [
                '200' => O::res('Restarted.', ['type' => 'object', 'properties' => ['unit' => ['type' => 'string'], 'scheduled' => ['type' => 'boolean']]], ['unit' => 'nimvor-scheduler.service', 'scheduled' => false]),
                '202' => O::res('Scheduled.', ['type' => 'object', 'properties' => ['unit' => ['type' => 'string'], 'scheduled' => ['type' => 'boolean']]], ['unit' => 'nimvor-panel.service', 'scheduled' => true]),
                '403' => O::err('Forbidden'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError'),
            ],
        ]],
    ],
];
