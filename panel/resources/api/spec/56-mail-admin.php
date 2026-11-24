<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$T = ['Mail server'];
$adm = 'Server administrators only (`403` for others).';
$obj = ['type' => 'object'];
$errs = ['403' => O::err('Forbidden'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')];
$read = ['403' => O::err('Forbidden'), '502' => O::err('AgentError')];
$n = ['name' => 'n', 'in' => 'path', 'required' => true, 'description' => 'The trusted host\'s position in the list (from 0).', 'schema' => ['type' => 'integer', 'minimum' => 0]];

$queue = ['total' => 2, 'counts' => ['deferred' => 1, 'hold' => 1], 'messages' => [['queue' => 'deferred', 'id' => '7506CE0062', 'arrival' => 1790000000, 'size' => 2048, 'sender' => 'shop@example.com', 'recipients' => ['x@nowhere.invalid'], 'reason' => 'Host or domain name not found']]];
$server = ['services' => [['unit' => 'postfix.service', 'name' => 'Postfix', 'active' => true, 'enabled' => true, 'version' => '3.8.6']], 'checks' => ['postfix' => '', 'dovecot' => ''], 'values' => ['postfix' => ['message_size_limit' => '52428800', 'maximal_queue_lifetime' => '5d'], 'dovecot' => ['login_greeting' => '']], 'labels' => ['postfix' => ['message_size_limit' => 'Largest message (MB)']]];
$grey = ['configured' => true, 'service' => ['active' => true, 'socket' => true, 'postfix' => true], 'providers' => [['id' => 'google', 'name' => 'Google (Gmail, Workspace)']], 'config' => ['enabled' => true, 'deferral' => 5, 'resend' => 240, 'expire' => 4320, 'spf_bypass' => true, 'auto_trust_new' => true, 'trusted' => [['range' => '192.0.2.0/24', 'comment' => 'office']], 'providers' => ['google' => ['trust' => true, 'auto_update' => true]]]];
$limits = ['configured' => true, 'service' => ['active' => true, 'postfix' => true], 'accounts' => ['blog', 'shop'], 'config' => ['enabled' => true, 'per_hour' => 200, 'accounts' => ['shop' => 50]], 'usage' => ['hour' => 1790000000, 'accounts' => [['uid' => 1003, 'name' => 'shop', 'sent' => 50, 'limit' => 50, 'blocked' => true]], 'events' => []]];

return [
    'tags' => [
        ['name' => 'Mail server', 'description' => "Administration of the mail server for server administrators: the delivery queue, Postfix and Dovecot settings, a troubleshooter, delivery reports, greylisting and the outgoing sending limits per website account. Mailboxes, aliases and filters are under *Email*.\n\nPostfix and Dovecot settings are checked by the mail server and the previous values come back if it refuses them. Every change is audited."],
    ],
    'paths' => [
        '/mail/admin/queue' => ['get' => [
            'operationId' => 'getMailQueue', 'tags' => $T, 'summary' => 'List the mail queue',
            'description' => 'Queued messages (at most 1000), with the queue each is in (`active`, `deferred`, `hold`, …) and, for deferred mail, the reason. ' . $adm,
            'x-permission' => 'server.view',
            'responses' => ['200' => O::res('The queue.', $obj, $queue)] + $read,
        ]],
        '/mail/admin/queue/{id}' => ['get' => [
            'operationId' => 'getQueuedMessage', 'tags' => $T, 'summary' => 'Read a queued message\'s headers',
            'description' => $adm,
            'x-permission' => 'server.view',
            'parameters' => [['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The queue ID.', 'schema' => ['type' => 'string']]],
            'responses' => ['200' => O::res('The headers.', $obj, ['id' => '7506CE0062', 'headers' => ['from' => 'Shop <shop@example.com>', 'subject' => 'Order']]), '404' => O::err('NotFound')] + $read,
        ]],
        '/mail/admin/queue/actions' => ['post' => [
            'operationId' => 'actOnMailQueue', 'tags' => $T, 'summary' => 'Act on queued messages',
            'description' => '`delete`, `retry` (deliver now), `requeue`, `hold` or `release` the listed `ids` (1–5000), or `flush`, which retries every deferred message and needs no ids. ' . $adm,
            'x-permission' => 'server.manage',
            'requestBody' => O::body(['type' => 'object', 'required' => ['action'], 'properties' => ['action' => ['type' => 'string', 'enum' => ['delete', 'retry', 'requeue', 'hold', 'release', 'flush']], 'ids' => ['type' => 'array', 'items' => ['type' => 'string']]]], ['action' => 'hold', 'ids' => ['7506CE0062']]),
            'responses' => ['200' => O::res('Done.', $obj, ['action' => 'hold', 'count' => 1])] + $errs,
        ]],
        '/mail/admin/server' => [
            'get' => [
                'operationId' => 'getMailServer', 'tags' => $T, 'summary' => 'Get the mail server\'s status and settings',
                'description' => 'Postfix, Dovecot and OpenDKIM (running, enabled, version), the result of their configuration checks, and the managed settings with their current `values` and `labels`. ' . $adm,
                'x-permission' => 'server.view',
                'responses' => ['200' => O::res('The status.', $obj, $server)] + $read,
            ],
            'patch' => [
                'operationId' => 'setMailServer', 'tags' => $T, 'summary' => 'Change Postfix and Dovecot settings',
                'description' => 'Only the settings you send change. The names must be among `values` of `GET /mail/admin/server`; each value is checked by the mail server, which puts the previous values back if its own check fails. ' . $adm,
                'x-permission' => 'server.manage',
                'requestBody' => O::body(['type' => 'object', 'properties' => ['postfix' => ['type' => 'object', 'additionalProperties' => ['type' => 'string']], 'dovecot' => ['type' => 'object', 'additionalProperties' => ['type' => 'string']]]], ['postfix' => ['message_size_limit' => '100']]),
                'responses' => ['200' => O::res('The status after the change.', $obj, $server)] + $errs,
            ],
        ],
        '/mail/admin/log' => ['get' => [
            'operationId' => 'getMailLog', 'tags' => $T, 'summary' => 'The latest mail log lines',
            'description' => $adm, 'x-permission' => 'server.view',
            'parameters' => [O::query('lines', 'How many lines (1–500).', ['type' => 'integer', 'minimum' => 1, 'maximum' => 500, 'default' => 100])],
            'responses' => ['200' => O::res('The lines.', $obj, ['source' => 'journal', 'lines' => [['time' => '2026-11-24T07:30:00Z', 'ident' => 'postfix/smtp', 'message' => 'to=<a@b.test>, status=sent']]])] + $read,
        ]],
        '/mail/admin/trace' => ['get' => [
            'operationId' => 'traceMailAddress', 'tags' => $T, 'summary' => 'Trace how mail for an address is delivered',
            'description' => 'Follows aliases and forwards step by step to the mailbox, or says why the address does not work. ' . $adm,
            'x-permission' => 'server.view',
            'parameters' => [['required' => true] + O::query('address', 'The email address.', ['type' => 'string'])],
            'responses' => ['200' => O::res('The steps.', $obj, ['address' => 'sales@example.com', 'steps' => [['depth' => 0, 'address' => 'sales@example.com', 'result' => 'forward', 'detail' => 'Forwarded to me@example.com.']]])] + $read + ['422' => O::err('ValidationFailed')],
        ]],
        '/mail/admin/repair' => ['post' => [
            'operationId' => 'repairMailPermissions', 'tags' => $T, 'summary' => 'Repair mailbox permissions',
            'description' => 'Checks the mailbox folders and files of one email domain (or all when `domain` is empty) and corrects their owner and permissions. Links and special files are skipped and listed. ' . $adm,
            'x-permission' => 'server.manage',
            'requestBody' => O::body(['type' => 'object', 'properties' => ['domain' => ['type' => 'string']]], ['domain' => 'example.com'], false),
            'responses' => ['200' => O::res('What was checked and fixed.', $obj, ['dirs' => 12, 'files' => 40, 'fixed' => 3, 'skipped' => []])] + $errs,
        ]],
        '/mail/admin/report' => ['get' => [
            'operationId' => 'getMailReport', 'tags' => $T, 'summary' => 'Delivery report from the mail logs',
            'description' => 'Totals, a time series, recent deliveries, the busiest local senders and domains. ' . $adm,
            'x-permission' => 'server.view',
            'parameters' => [O::query('range', 'The period.', ['type' => 'string', 'enum' => ['1h', '24h', '7d', '30d'], 'default' => '24h']), O::query('address', 'Only this sender or recipient.', ['type' => 'string']), O::query('status', 'Only deliveries with this status (`sent`, `deferred`, `bounced`, …).', ['type' => 'string'])],
            'responses' => ['200' => O::res('The report.', $obj, ['range' => '24h', 'source' => 'journal', 'report' => ['totals' => ['received' => 12, 'sent' => 7, 'bounced' => 2], 'deliveries' => []]]), '422' => O::err('ValidationFailed')] + $read,
        ]],
        '/mail/admin/greylisting' => [
            'get' => [
                'operationId' => 'getGreylisting', 'tags' => $T, 'summary' => 'Get greylisting',
                'description' => 'Settings, trusted hosts, trusted mail providers and whether the service runs. ' . $adm,
                'x-permission' => 'server.view',
                'responses' => ['200' => O::res('The state.', $obj, $grey)] + $read,
            ],
            'patch' => [
                'operationId' => 'setGreylisting', 'tags' => $T, 'summary' => 'Change greylisting settings',
                'description' => 'Only what is sent changes. `deferral`: minutes a new sender is refused (1–1440); `resend`: how long the retry is accepted (1–10080); `expire`: how long a record is kept (60–525600). ' . $adm,
                'x-permission' => 'server.manage',
                'requestBody' => O::body(['type' => 'object', 'properties' => ['enabled' => ['type' => 'boolean'], 'deferral' => ['type' => 'integer'], 'resend' => ['type' => 'integer'], 'expire' => ['type' => 'integer'], 'spf_bypass' => ['type' => 'boolean', 'description' => 'Skip greylisting for senders whose SPF record allows the connecting address.'], 'auto_trust_new' => ['type' => 'boolean', 'description' => 'Trust a sender once its retry has been accepted.']]], ['deferral' => 10]),
                'responses' => ['200' => O::res('The state.', $obj, $grey)] + $errs,
            ],
        ],
        '/mail/admin/greylisting/trusted' => ['post' => [
            'operationId' => 'addGreylistTrustedHost', 'tags' => $T, 'summary' => 'Trust an address or range',
            'description' => 'Mail from a trusted host is never greylisted. `range` is an IPv4/IPv6 address or CIDR range. ' . $adm,
            'x-permission' => 'server.manage',
            'requestBody' => O::body(['type' => 'object', 'required' => ['range'], 'properties' => ['range' => ['type' => 'string'], 'comment' => ['type' => 'string']]], ['range' => '192.0.2.0/24', 'comment' => 'office']),
            'responses' => ['201' => O::res('The state.', $obj, $grey)] + $errs,
        ]],
        '/mail/admin/greylisting/trusted/{n}' => [
            'put' => [
                'operationId' => 'saveGreylistTrustedHost', 'tags' => $T, 'summary' => 'Replace a trusted host',
                'description' => $adm, 'x-permission' => 'server.manage', 'parameters' => [$n],
                'requestBody' => O::body(['type' => 'object', 'required' => ['range'], 'properties' => ['range' => ['type' => 'string'], 'comment' => ['type' => 'string']]], ['range' => '192.0.2.0/25']),
                'responses' => ['200' => O::res('The state.', $obj, $grey), '404' => O::err('NotFound')] + $errs,
            ],
            'delete' => [
                'operationId' => 'deleteGreylistTrustedHost', 'tags' => $T, 'summary' => 'Remove a trusted host',
                'description' => 'Later entries move up by one position. ' . $adm, 'x-permission' => 'server.manage', 'parameters' => [$n],
                'responses' => ['200' => O::res('The state.', $obj, $grey), '404' => O::err('NotFound')] + $errs,
            ],
        ],
        '/mail/admin/greylisting/providers' => ['put' => [
            'operationId' => 'setGreylistProviders', 'tags' => $T, 'summary' => 'Choose the trusted mail providers',
            'description' => 'Large providers whose sending addresses are trusted (read from their SPF records). Send the provider ids to `trust` and to keep up to date in `auto_update`; a provider not listed is switched off. ' . $adm,
            'x-permission' => 'server.manage',
            'requestBody' => O::body(['type' => 'object', 'properties' => ['trust' => ['type' => 'array', 'items' => ['type' => 'string']], 'auto_update' => ['type' => 'array', 'items' => ['type' => 'string']]]], ['trust' => ['google'], 'auto_update' => ['google']]),
            'responses' => ['200' => O::res('The state.', $obj, $grey)] + $errs,
        ]],
        '/mail/admin/greylisting/providers/update' => ['post' => [
            'operationId' => 'updateGreylistProviders', 'tags' => $T, 'summary' => 'Update the providers\' address ranges now',
            'description' => 'Reads the providers\' SPF records again. ' . $adm, 'x-permission' => 'server.manage',
            'responses' => ['200' => O::res('The state.', $obj, $grey)] + $errs,
        ]],
        '/mail/admin/greylisting/report' => ['get' => [
            'operationId' => 'getGreylistReport', 'tags' => $T, 'summary' => 'Greylisting records',
            'description' => 'The records of (address, sender, recipient) triplets with their status: `deferring`, `waiting` (retry accepted), `passed`, `expired`. ' . $adm,
            'x-permission' => 'server.view',
            'parameters' => [
                O::query('search', 'Part of an address, sender or recipient.', ['type' => 'string']),
                O::query('status', 'Only this status.', ['type' => 'string', 'enum' => ['deferring', 'waiting', 'passed', 'expired']]),
                O::query('sort', 'Sort by.', ['type' => 'string', 'enum' => ['ip', 'from', 'to', 'deferred', 'accepted', 'created', 'block_expire', 'must_retry', 'record_expire'], 'default' => 'created']),
                O::query('dir', 'Direction.', ['type' => 'string', 'enum' => ['asc', 'desc'], 'default' => 'desc']),
                O::query('page', 'Page, from 1.', ['type' => 'integer', 'minimum' => 1, 'default' => 1]),
                O::query('per', 'Records per page.', ['type' => 'integer', 'enum' => [25, 50, 100, 250], 'default' => 50]),
            ],
            'responses' => ['200' => O::res('The records.', $obj, ['query' => ['page' => 1, 'per' => 50], 'running' => true, 'total' => 120, 'counts' => ['deferring' => 100], 'rows' => [['ip' => '203.0.113.9', 'from' => 'alice@sender.test', 'to' => 'bob@example.com', 'status' => 'deferring']]])] + $read,
        ]],
        '/mail/admin/greylisting/cleanup' => ['post' => [
            'operationId' => 'cleanGreylistRecords', 'tags' => $T, 'summary' => 'Remove greylisting records',
            'description' => '`op`: `delete` the listed `triplets` (each `{ip, from, to}`, at most 1000), `purge` the expired records or `delete_all`. ' . $adm,
            'x-permission' => 'server.manage',
            'requestBody' => O::body(['type' => 'object', 'required' => ['op'], 'properties' => ['op' => ['type' => 'string', 'enum' => ['delete', 'purge', 'delete_all']], 'triplets' => ['type' => 'array', 'items' => ['type' => 'object', 'properties' => ['ip' => ['type' => 'string'], 'from' => ['type' => 'string'], 'to' => ['type' => 'string']]]]]], ['op' => 'purge']),
            'responses' => ['200' => O::res('How many were removed.', $obj, ['op' => 'purge', 'removed' => 2])] + $errs,
        ]],
        '/mail/admin/sending-limits' => [
            'get' => [
                'operationId' => 'getMailSendingLimits', 'tags' => $T, 'summary' => 'Get the outgoing mail limits',
                'description' => 'Recipients per website account and hour (websites\' `mail()`, cron jobs), this hour\'s use and which accounts are blocked. ' . $adm,
                'x-permission' => 'server.view',
                'responses' => ['200' => O::res('The limits and the use.', $obj, $limits)] + $read,
            ],
            'patch' => [
                'operationId' => 'setMailSendingLimits', 'tags' => $T, 'summary' => 'Change the outgoing mail limits',
                'description' => 'Only what is sent changes. `per_hour` is the default for every website account (0 means no limit). In `accounts`, a number sets that account\'s limit and `null` removes it. Turning the limits on first re-applies every PHP website so its mail goes through NimVor; if one cannot be updated the limits stay off (`409`). ' . $adm,
                'x-permission' => 'server.manage',
                'requestBody' => O::body(['type' => 'object', 'properties' => ['enabled' => ['type' => 'boolean'], 'per_hour' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 1000000], 'accounts' => ['type' => 'object', 'additionalProperties' => ['type' => ['integer', 'null']]]]], ['per_hour' => 300, 'accounts' => ['shop' => 50, 'blog' => null]]),
                'responses' => ['200' => O::res('The limits and the use.', $obj, $limits)] + $errs,
            ],
        ],
        '/mail/admin/sending-limits/reset' => ['post' => [
            'operationId' => 'resetMailSendingLimits', 'tags' => $T, 'summary' => 'Let a blocked account send again',
            'description' => 'Resets this hour\'s count of one website account, or of every account when `account` is empty. ' . $adm,
            'x-permission' => 'server.manage',
            'requestBody' => O::body(['type' => 'object', 'properties' => ['account' => ['type' => 'string']]], ['account' => 'shop'], false),
            'responses' => ['200' => O::res('Done.', $obj, ['reset' => 'shop']), '404' => O::err('NotFound')] + $errs,
        ]],
    ],
];
