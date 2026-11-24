<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$T = ['ModSecurity'];
$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The website\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$rule = ['index' => 0, 'id' => 10001, 'phase' => '2', 'action' => 'deny', 'message' => 'Block probes', 'comment' => 'Block probes', 'rule' => "SecRule REQUEST_URI \"@contains /probe\" \"id:10001,phase:2,deny,status:403,msg:'Block probes'\"", 'sha256' => str_repeat('a', 64)];
$status = [
    'enabled' => true, 'active' => true, 'module_loaded' => true, 'detection_only' => false,
    'connector' => ['state' => 'functional', 'reason' => ''],
    'crs' => ['enabled' => true, 'installed' => true, 'version' => '4.0.0', 'paranoia' => 1, 'inbound_threshold' => 5, 'outbound_threshold' => 4],
    'custom' => ['enabled' => true, 'rules' => 1], 'packages_available' => true, 'audit_log_bytes' => 20480,
    'vendors' => [['id' => 'crs', 'name' => 'OWASP Core Rule Set', 'description' => 'General attack detection.']],
];
$siteSample = ['domain' => 'shop.example.com', 'mode' => 'on', 'vendors_off' => [], 'disabled_rules' => [], 'available' => true, 'detection_only_server' => false];
$event = ['time' => '2026-11-24T07:30:00Z', 'host' => 'shop.example.com', 'client_ip' => '203.0.113.9', 'method' => 'GET', 'uri' => '/?q=<script>', 'status' => 403, 'engine' => 'On', 'rules' => [['id' => '941100', 'message' => 'XSS Attack Detected', 'severity' => 'CRITICAL']]];
$job = ['type' => 'object', 'properties' => ['job_id' => ['type' => 'string', 'description' => 'Follow it with `GET /jobs/{id}`.']]];
$errs = ['403' => O::err('Forbidden'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')];
$adm = 'Server administrators only (`403` for others).';
$ruleRef = O::ref('ModSecurityRule');

return [
    'tags' => [
        ['name' => 'ModSecurity', 'description' => "The ModSecurity web application firewall. Server administrators set it up (on/off, the OWASP Core Rule Set, custom rules, installation, updates); a website's owner may switch the website between off, detection only and on, but cannot change rule vendors or single rules.\n\nEvery change is checked by the web server (`nginx -t`) and the previous configuration is kept when it refuses. Installation, updates and rollbacks run in the background and answer `202` with a `job_id`."],
    ],
    'schemas' => [
        'ModSecurityRule' => [
            'type' => 'object',
            'properties' => [
                'index' => ['type' => 'integer', 'description' => 'Position in the custom rules file; it changes when an earlier rule is deleted.'],
                'id' => ['type' => ['integer', 'null'], 'description' => 'The rule\'s `id:` (custom rules use 10000–99999).'],
                'phase' => ['type' => 'string'], 'action' => ['type' => 'string'], 'message' => ['type' => 'string'],
                'comment' => ['type' => 'string', 'description' => 'The comment lines above the rule.'],
                'rule' => ['type' => 'string', 'description' => 'The rule text (directive and continuation lines).'],
                'sha256' => ['type' => 'string', 'description' => 'SHA-256 of `rule`. Send it back to change or delete the rule, so a rule edited meanwhile is never overwritten.'],
            ],
        ],
    ],
    'paths' => [
        '/security/modsec' => [
            'get' => [
                'operationId' => 'getModSecurity', 'tags' => $T, 'summary' => 'Get ModSecurity\'s state',
                'description' => 'Whether it is on and loaded, the Nginx connector\'s state, the Core Rule Set and the custom rules. ' . $adm,
                'x-permission' => 'security.view',
                'responses' => ['200' => O::res('The state.', ['type' => 'object'], $status), '403' => O::err('Forbidden'), '502' => O::err('AgentError')],
            ],
            'patch' => [
                'operationId' => 'configureModSecurity', 'tags' => $T, 'summary' => 'Change the server-wide settings',
                'description' => 'Only what is sent changes. Turning it on needs the connector to be installed and usable. With `detection_only`, websites set to block only log until blocking is allowed. ' . $adm,
                'x-permission' => 'security.manage',
                'requestBody' => O::body(['type' => 'object', 'properties' => [
                    'enabled' => ['type' => 'boolean'], 'detection_only' => ['type' => 'boolean'],
                    'crs' => ['type' => 'object', 'properties' => ['enabled' => ['type' => 'boolean'], 'paranoia' => ['type' => 'integer', 'minimum' => 1, 'maximum' => 4], 'inbound_threshold' => ['type' => 'integer', 'minimum' => 2, 'maximum' => 1000], 'outbound_threshold' => ['type' => 'integer', 'minimum' => 2, 'maximum' => 1000]]],
                    'custom' => ['type' => 'object', 'properties' => ['enabled' => ['type' => 'boolean']]],
                ]], ['enabled' => true, 'crs' => ['paranoia' => 2]]),
                'responses' => ['200' => O::res('The state after the change.', ['type' => 'object'], $status)] + $errs,
            ],
        ],
        '/security/modsec/rules' => [
            'get' => [
                'operationId' => 'listModSecurityRules', 'tags' => $T, 'summary' => 'List the custom rules',
                'description' => '`enabled` says whether the server-wide "custom rules" switch is on; `next_id` is a free rule id. ' . $adm,
                'x-permission' => 'security.view',
                'responses' => ['200' => O::res('The rules.', ['type' => 'object', 'properties' => ['rules' => ['type' => 'array', 'items' => $ruleRef], 'enabled' => ['type' => 'boolean'], 'next_id' => ['type' => 'integer']]], ['rules' => [$rule], 'enabled' => true, 'next_id' => 10002]), '403' => O::err('Forbidden'), '502' => O::err('AgentError')],
            ],
            'post' => [
                'operationId' => 'addModSecurityRule', 'tags' => $T, 'summary' => 'Add a custom rule',
                'description' => 'The whole set is checked by Nginx; nothing changes when it refuses. The rules file is at most 64 KiB. ' . $adm,
                'x-permission' => 'security.manage',
                'requestBody' => O::body(['type' => 'object', 'required' => ['rule'], 'properties' => ['rule' => ['type' => 'string'], 'comment' => ['type' => 'string', 'maxLength' => 500]]], ['rule' => $rule['rule'], 'comment' => 'Block probes']),
                'responses' => ['201' => O::res('The rule added.', ['type' => 'object', 'properties' => ['rule' => $ruleRef, 'rules' => ['type' => 'integer'], 'custom_enabled' => ['type' => 'boolean']]], ['rule' => $rule, 'rules' => 1, 'custom_enabled' => true])] + $errs,
            ],
        ],
        '/security/modsec/rules/{index}' => [
            'put' => [
                'operationId' => 'saveModSecurityRule', 'tags' => $T, 'summary' => 'Replace a custom rule',
                'description' => 'Send the `sha256` you read; if the rule changed meanwhile the answer is `409`. ' . $adm,
                'x-permission' => 'security.manage',
                'parameters' => [['name' => 'index', 'in' => 'path', 'required' => true, 'description' => 'The rule\'s position.', 'schema' => ['type' => 'integer', 'minimum' => 0]]],
                'requestBody' => O::body(['type' => 'object', 'required' => ['rule', 'sha256'], 'properties' => ['rule' => ['type' => 'string'], 'comment' => ['type' => 'string'], 'sha256' => ['type' => 'string']]], ['rule' => $rule['rule'], 'sha256' => $rule['sha256']]),
                'responses' => ['200' => O::res('The rule.', ['type' => 'object', 'properties' => ['rule' => $ruleRef, 'rules' => ['type' => 'integer'], 'custom_enabled' => ['type' => 'boolean']]], ['rule' => $rule, 'rules' => 1, 'custom_enabled' => true]), '404' => O::err('NotFound')] + $errs,
            ],
            'delete' => [
                'operationId' => 'deleteModSecurityRule', 'tags' => $T, 'summary' => 'Delete a custom rule',
                'description' => 'Pass the rule\'s `sha256` as a query parameter. ' . $adm,
                'x-permission' => 'security.manage',
                'parameters' => [['name' => 'index', 'in' => 'path', 'required' => true, 'description' => 'The rule\'s position.', 'schema' => ['type' => 'integer', 'minimum' => 0]], O::query('sha256', 'The rule\'s SHA-256, as listed.', ['type' => 'string'])],
                'responses' => ['200' => O::res('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'boolean'], 'rules' => ['type' => 'integer']]], ['deleted' => true, 'rules' => 0]), '404' => O::err('NotFound'), '403' => O::err('Forbidden'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
            ],
        ],
        '/security/modsec/events' => ['get' => [
            'operationId' => 'listModSecurityEvents', 'tags' => $T, 'summary' => 'Recent firewall events',
            'description' => 'Requests ModSecurity logged or blocked, newest first. ' . $adm,
            'x-permission' => 'security.view',
            'parameters' => [O::query('limit', 'How many events (1–200).', ['type' => 'integer', 'minimum' => 1, 'maximum' => 200, 'default' => 50])],
            'responses' => ['200' => O::res('The events.', ['type' => 'object', 'properties' => ['events' => ['type' => 'array', 'items' => ['type' => 'object']]]], ['events' => [$event]]), '403' => O::err('Forbidden'), '502' => O::err('AgentError')],
        ]],
        '/security/modsec/update' => ['post' => [
            'operationId' => 'updateModSecurityRules', 'tags' => $T, 'summary' => 'Update the rule packages',
            'description' => 'Runs in the background. ' . $adm,
            'x-permission' => 'security.manage',
            'responses' => ['202' => O::res('The background job.', $job, ['job_id' => 'job_123'])] + $errs,
        ]],
        '/security/modsec/install' => ['post' => [
            'operationId' => 'installModSecurity', 'tags' => $T, 'summary' => 'Install or upgrade the Nginx connector',
            'description' => 'A transaction on the server: everything is backed up first and rolled back if a step fails. `method`: `auto` uses packages where possible, `source` builds the connector. ' . $adm,
            'x-permission' => 'security.manage',
            'requestBody' => O::body(['type' => 'object', 'properties' => ['method' => ['type' => 'string', 'enum' => ['auto', 'source'], 'default' => 'auto'], 'upgrade' => ['type' => 'boolean'], 'nginx_source_sha256' => ['type' => 'string', 'description' => 'SHA-256 of the Nginx source, for `source`.']]], ['method' => 'auto'], false),
            'responses' => ['202' => O::res('The background job.', $job, ['job_id' => 'job_123'])] + $errs,
        ]],
        '/security/modsec/crs' => ['post' => [
            'operationId' => 'installModSecurityCrs', 'tags' => $T, 'summary' => 'Install the OWASP Core Rule Set',
            'description' => 'It starts in detection only. ' . $adm,
            'x-permission' => 'security.manage',
            'responses' => ['202' => O::res('The background job.', $job, ['job_id' => 'job_123'])] + $errs,
        ]],
        '/security/modsec/installs' => ['get' => [
            'operationId' => 'listModSecurityInstalls', 'tags' => $T, 'summary' => 'List installations',
            'description' => 'Installation records, newest first, each with an `id` you can roll back. ' . $adm,
            'x-permission' => 'security.view',
            'responses' => ['200' => O::res('The records.', ['type' => 'object', 'properties' => ['installs' => ['type' => 'array', 'items' => ['type' => 'object']]]], ['installs' => [['id' => '20261124T073000Z-ab12', 'method' => 'auto']]]), '403' => O::err('Forbidden'), '502' => O::err('AgentError')],
        ]],
        '/security/modsec/installs/{id}/rollback' => ['post' => [
            'operationId' => 'rollbackModSecurityInstall', 'tags' => $T, 'summary' => 'Roll back an installation',
            'description' => 'Runs in the background; nothing changes if Nginx would not accept the result. ' . $adm,
            'x-permission' => 'security.manage',
            'parameters' => [['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The installation\'s id.', 'schema' => ['type' => 'string']]],
            'responses' => ['202' => O::res('The background job.', $job, ['job_id' => 'job_123']), '404' => O::err('NotFound')] + $errs,
        ]],
        '/websites/{id}/modsec' => [
            'get' => [
                'operationId' => 'getWebsiteModSecurity', 'tags' => $T, 'summary' => 'Get a website\'s web firewall',
                'description' => '`mode` is `off`, `detect` (log only) or `on` (block attacks). `available` is false when ModSecurity is not loaded on the server.',
                'x-permission' => 'websites.view', 'parameters' => [$id],
                'responses' => ['200' => O::res('The website\'s setting.', ['type' => 'object'], $siteSample), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
            ],
            'put' => [
                'operationId' => 'setWebsiteModSecurity', 'tags' => $T, 'summary' => 'Set a website\'s web firewall',
                'description' => 'Everyone who manages the website sets `mode`. `vendors_off` (`crs`, `custom`) and `disabled_rules` (up to 200 rule ids) are for server administrators; others get `422`. Only what is sent changes.',
                'x-permission' => 'websites.manage', 'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'properties' => ['mode' => ['type' => 'string', 'enum' => ['off', 'detect', 'on']], 'vendors_off' => ['type' => 'array', 'items' => ['type' => 'string', 'enum' => ['crs', 'custom']]], 'disabled_rules' => ['type' => 'array', 'items' => ['type' => 'integer']]]], ['mode' => 'detect']),
                'responses' => ['200' => O::res('The website\'s setting.', ['type' => 'object'], ['mode' => 'detect'] + $siteSample), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/modsec/events' => ['get' => [
            'operationId' => 'listWebsiteModSecurityEvents', 'tags' => $T, 'summary' => 'A website\'s firewall events',
            'x-permission' => 'websites.view', 'parameters' => [$id, O::query('limit', 'How many events (1–200).', ['type' => 'integer', 'minimum' => 1, 'maximum' => 200, 'default' => 50])],
            'responses' => ['200' => O::res('The events.', ['type' => 'object', 'properties' => ['events' => ['type' => 'array', 'items' => ['type' => 'object']]]], ['events' => [$event]]), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
        ]],
    ],
];
