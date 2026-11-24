<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$pending = ['id' => 'abc123abc123', 'summary' => 'firewall: allow proto tcp port 8080', 'kind' => 'firewall', 'deadline' => '2026-11-24T10:02:00Z'];
$changed = ['pending' => $pending, 'message' => 'Applied. Confirm it within 2 minutes with POST /security/confirm, otherwise it is undone automatically.'];
$rule = ['spec' => 'allow 8443/tcp', 'action' => 'allow', 'port' => '8443', 'proto' => 'tcp', 'from' => '', 'comment' => '', 'protected' => 'NimVor panel (port 8443)'];
$changedRef = O::ref('GuardedChange');
$guarded = ['202' => O::res('The change was applied and now waits for confirmation. `pending` is null when nothing needed to change.', $changedRef, $changed), '403' => O::err('Forbidden'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')];

return [
    'tags' => [
        ['name' => 'Firewall', 'description' => "The server's firewall (UFW or firewalld), its protections and the confirmation of changes. Needs the security permissions, which hosting customers do not have.\n\n**Every change is guarded.** The agent applies it, then undoes it automatically after **two minutes** unless you confirm it with `POST /security/confirm`. So a rule that cuts you off heals itself: check that you can still reach the server, then confirm. The agent also refuses a change that would remove SSH or the panel's own port (`502`, with a `reason`). Only one change can wait at a time.\n\nA rule is named by its `spec`, exactly as `GET /security/firewall` lists it."],
    ],
    'schemas' => [
        'GuardedChange' => [
            'type' => 'object',
            'properties' => [
                'pending' => ['type' => ['object', 'null'], 'description' => 'The change waiting for confirmation; null when nothing needed to change.', 'properties' => [
                    'id' => ['type' => 'string', 'description' => 'Pass it to `POST /security/confirm` or `/security/undo`.'],
                    'summary' => ['type' => 'string'], 'kind' => ['type' => 'string', 'enum' => ['firewall', 'ssh']],
                    'deadline' => ['type' => ['string', 'null'], 'format' => 'date-time', 'description' => 'When the change is undone if it is not confirmed.'],
                ]],
                'message' => ['type' => 'string'],
            ],
        ],
    ],
    'paths' => [
        '/security/firewall' => [
            'get' => [
                'operationId' => 'getFirewall',
                'tags' => ['Firewall'],
                'summary' => 'Get the firewall',
                'description' => 'Whether the firewall is installed and on, its default policies and rules, the SSH port and whether it is allowed, and any change waiting for confirmation. A rule with `protected` set (the panel\'s own port) cannot be deleted.',
                'x-permission' => 'security.view',
                'responses' => ['200' => O::res('The firewall.', ['type' => 'object', 'properties' => [
                    'installed' => ['type' => 'boolean'], 'active' => ['type' => 'boolean'], 'backend' => ['type' => 'string', 'enum' => ['ufw', 'firewalld']],
                    'default_incoming' => ['type' => ['string', 'null']], 'default_outgoing' => ['type' => ['string', 'null']],
                    'ssh_port' => ['type' => 'integer'], 'ssh_allowed' => ['type' => 'boolean'],
                    'rules' => ['type' => 'array', 'items' => ['type' => 'object', 'properties' => [
                        'spec' => ['type' => 'string', 'description' => 'The rule as the firewall shows it; use it to delete the rule.'],
                        'action' => ['type' => 'string'], 'port' => ['type' => 'string'], 'proto' => ['type' => 'string'],
                        'from' => ['type' => 'string', 'description' => 'The source address; empty means anywhere.'], 'comment' => ['type' => 'string'],
                        'protected' => ['type' => ['string', 'null'], 'description' => 'Why the rule cannot be deleted, if so.'],
                    ]]],
                    'pending' => ['type' => ['object', 'null'], 'description' => 'A change waiting for confirmation.'],
                ]], ['installed' => true, 'active' => true, 'backend' => 'ufw', 'default_incoming' => 'deny', 'default_outgoing' => 'allow', 'ssh_port' => 22, 'ssh_allowed' => true, 'rules' => [$rule], 'pending' => null]), '403' => O::err('Forbidden'), '502' => O::err('AgentError')],
            ],
            'put' => [
                'operationId' => 'setFirewallEnabled',
                'tags' => ['Firewall'],
                'summary' => 'Turn the firewall on or off',
                'description' => 'Turning it on first opens SSH, the panel and (with `allow_services`) the ports of the services that are listening, and optionally every port for `allow_from`, all in the same guarded change. Confirm it within two minutes.',
                'x-permission' => 'security.manage',
                'requestBody' => O::body(['type' => 'object', 'required' => ['enabled'], 'properties' => [
                    'enabled' => ['type' => 'boolean'],
                    'allow_services' => ['type' => 'boolean', 'default' => true, 'description' => 'When turning on: also allow the ports of the services that are listening.'],
                    'allow_from' => ['type' => 'string', 'description' => 'When turning on: an IP address that may reach every port (usually yours). It is also exempted from brute-force protection.', 'examples' => ['203.0.113.7']],
                ]], ['enabled' => true, 'allow_services' => true, 'allow_from' => '203.0.113.7']),
                'responses' => $guarded,
            ],
        ],
        '/security/firewall/rules' => [
            'post' => [
                'operationId' => 'addFirewallRule',
                'tags' => ['Firewall'],
                'summary' => 'Add a firewall rule',
                'description' => 'Adds a rule for a port or a range. Confirm it within two minutes.',
                'x-permission' => 'security.manage',
                'requestBody' => O::body(['type' => 'object', 'required' => ['port'], 'properties' => [
                    'action' => ['type' => 'string', 'enum' => ['allow', 'deny', 'reject', 'limit'], 'default' => 'allow', 'description' => '`limit` allows but rate-limits repeated connections.'],
                    'port' => ['type' => 'string', 'description' => 'A port (`8080`) or range (`8000:8100`).', 'examples' => ['8080']],
                    'proto' => ['type' => 'string', 'enum' => ['', 'tcp', 'udp'], 'description' => 'Empty means both.'],
                    'from' => ['type' => 'string', 'description' => 'A source IP address or network; empty means anywhere.', 'examples' => ['203.0.113.0/24']],
                    'comment' => ['type' => 'string', 'maxLength' => 100],
                ]], ['action' => 'allow', 'port' => '8080', 'proto' => 'tcp', 'comment' => 'app']),
                'responses' => $guarded,
            ],
            'delete' => [
                'operationId' => 'deleteFirewallRule',
                'tags' => ['Firewall'],
                'summary' => 'Delete a firewall rule',
                'description' => 'Deletes the rule with this `spec`. SSH and the panel\'s own port cannot be removed. Confirm it within two minutes.',
                'x-permission' => 'security.manage',
                'parameters' => [['name' => 'spec', 'in' => 'query', 'required' => true, 'description' => 'The rule, exactly as `GET /security/firewall` lists it (URL-encode it).', 'schema' => ['type' => 'string'], 'example' => 'allow 8080/tcp']],
                'responses' => $guarded,
            ],
        ],
        '/security/firewall/protection' => [
            'get' => [
                'operationId' => 'getFirewallProtection',
                'tags' => ['Firewall'],
                'summary' => 'Get the flood and connection protections',
                'description' => 'The applied configuration, whether its rules are loaded, and how many packets each protection dropped.',
                'x-permission' => 'security.view',
                'responses' => ['200' => O::res('The protections.', ['type' => 'object', 'properties' => [
                    'available' => ['type' => 'boolean'], 'active' => ['type' => 'boolean'],
                    'config' => ['type' => 'object'], 'dropped' => ['type' => 'object', 'description' => 'Packets dropped by each protection.'],
                ]], ['available' => true, 'active' => true, 'config' => ['enabled' => true, 'invalid' => true, 'syn_rate' => 80, 'icmp_rate' => 0, 'conn_limits' => [['port' => 22, 'proto' => 'tcp', 'max' => 10]], 'port_flood' => [['port' => 53, 'proto' => 'udp', 'max' => 600]], 'exempt' => ['203.0.113.4']], 'dropped' => ['syn' => 1234]]), '403' => O::err('Forbidden'), '502' => O::err('AgentError')],
            ],
            'put' => [
                'operationId' => 'setFirewallProtection',
                'tags' => ['Firewall'],
                'summary' => 'Set the flood and connection protections',
                'description' => 'Replaces the whole configuration (send every field you want to keep). The protections only drop or reject traffic, next to UFW or firewalld. Confirm it within two minutes.',
                'x-permission' => 'security.manage',
                'requestBody' => O::body(['type' => 'object', 'properties' => [
                    'enabled' => ['type' => 'boolean'],
                    'invalid' => ['type' => 'boolean', 'description' => 'Drop invalid packets.'],
                    'syn_rate' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 100000, 'description' => 'SYN packets per second allowed; 0 means no limit.'],
                    'icmp_rate' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 10000, 'description' => 'Ping packets per second allowed; 0 means no limit.'],
                    'conn_limits' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'Concurrent connections per address, as `port:max`.', 'examples' => [['22:10', '443:150']]],
                    'port_flood' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'New connections per minute per address, as `proto/port:max` (proto defaults to tcp).', 'examples' => [['tcp/80:300', 'udp/53:600']]],
                    'exempt' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'Addresses and networks the protections never apply to.', 'examples' => [['203.0.113.4', '10.0.0.0/8']]],
                ]], ['enabled' => true, 'invalid' => true, 'syn_rate' => 100, 'conn_limits' => ['22:10', '443:150'], 'port_flood' => ['tcp/80:300'], 'exempt' => ['203.0.113.4']]),
                'responses' => $guarded,
            ],
        ],
        '/security/pending' => ['get' => [
            'operationId' => 'getPendingSecurityChange',
            'tags' => ['Firewall'],
            'summary' => 'The change waiting for confirmation',
            'description' => 'A firewall or SSH change that was applied and will be undone at its `deadline` unless confirmed. Null when nothing waits.',
            'x-permission' => 'security.view',
            'responses' => ['200' => O::res('The pending change.', ['type' => 'object', 'properties' => ['pending' => ['type' => ['object', 'null']]]], ['pending' => $pending]), '403' => O::err('Forbidden')],
        ]],
        '/security/confirm' => ['post' => [
            'operationId' => 'confirmSecurityChange',
            'tags' => ['Firewall'],
            'summary' => 'Keep the pending change',
            'description' => 'Confirms the change so it stays. Do this only after checking that you can still reach the server.',
            'x-permission' => 'security.manage',
            'requestBody' => O::body(['type' => 'object', 'required' => ['id'], 'properties' => ['id' => ['type' => 'string', 'description' => 'The `pending.id`.']]], ['id' => 'abc123abc123']),
            'responses' => ['200' => O::res('Confirmed.', ['type' => 'object', 'properties' => ['confirmed' => ['type' => 'string']]], ['confirmed' => 'abc123abc123']), '403' => O::err('Forbidden'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
        ]],
        '/security/undo' => ['post' => [
            'operationId' => 'undoSecurityChange',
            'tags' => ['Firewall'],
            'summary' => 'Undo the pending change now',
            'description' => 'Reverts the change at once instead of waiting for the deadline.',
            'x-permission' => 'security.manage',
            'requestBody' => O::body(['type' => 'object', 'required' => ['id'], 'properties' => ['id' => ['type' => 'string', 'description' => 'The `pending.id`.']]], ['id' => 'abc123abc123']),
            'responses' => ['200' => O::res('Undone.', ['type' => 'object', 'properties' => ['undone' => ['type' => 'string']]], ['undone' => 'abc123abc123']), '403' => O::err('Forbidden'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
        ]],
    ],
];
