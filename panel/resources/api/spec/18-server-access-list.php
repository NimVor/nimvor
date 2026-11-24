<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$T = ['Web protection'];
$adm = 'Server administrators only (`403` for others).';
$rule = ['kind' => 'ip', 'action' => 'deny', 'value' => '203.0.113.0/24', 'note' => 'scanner'];
$listRes = ['type' => 'object', 'properties' => [
    'status' => ['type' => 'integer', 'enum' => [403, 404, 444], 'description' => 'What a refused visitor gets: 403, 404, or 444 (the connection is closed without an answer).'],
    'max' => ['type' => 'integer', 'description' => 'The most entries the server-wide list can have.'],
    'rules' => ['type' => 'array', 'items' => O::ref('AccessRule')],
]];
$sample = ['status' => 403, 'max' => 1000, 'rules' => [$rule]];
$errs = ['403' => O::err('Forbidden'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')];

return [
    'paths' => [
        '/security/access-list' => [
            'get' => [
                'operationId' => 'getServerAccessList', 'tags' => $T, 'summary' => 'Get the server-wide allow/deny list',
                'description' => 'Entries by address, browser or URL that apply to **every** website on the server, before each website\'s own list. An `allow` entry lets that visitor through even when another entry or a country restriction would refuse them. ' . $adm,
                'x-permission' => 'security.view',
                'responses' => ['200' => O::res('The list.', $listRes, $sample), '403' => O::err('Forbidden'), '502' => O::err('AgentError')],
            ],
            'post' => [
                'operationId' => 'addServerAccessRule', 'tags' => $T, 'summary' => 'Add an entry to the server-wide list',
                'description' => 'Adds the entry, or changes the action and note of the entry with the same kind and value. The web server configuration is checked before it is reloaded and the change is undone if it refuses. Returns the whole list. ' . $adm,
                'x-permission' => 'security.manage',
                'requestBody' => O::body(['type' => 'object', 'required' => ['kind', 'action', 'value'], 'properties' => ['kind' => ['type' => 'string', 'enum' => ['ip', 'ua', 'url']], 'action' => ['type' => 'string', 'enum' => ['deny', 'allow']], 'value' => ['type' => 'string'], 'note' => ['type' => 'string', 'maxLength' => 200]]], $rule),
                'responses' => ['201' => O::res('The list.', $listRes, $sample)] + $errs,
            ],
            'delete' => [
                'operationId' => 'deleteServerAccessRule', 'tags' => $T, 'summary' => 'Remove an entry from the server-wide list',
                'description' => 'Name the entry by its `kind` and `value`, as the list shows them, in the query string. Returns the list. ' . $adm,
                'x-permission' => 'security.manage',
                'parameters' => [['required' => true] + O::query('kind', 'The entry\'s kind (`ip`, `ua`, `url`).', ['type' => 'string', 'enum' => ['ip', 'ua', 'url']]), ['required' => true] + O::query('value', 'The entry\'s value.', ['type' => 'string'])],
                'responses' => ['200' => O::res('The list.', $listRes, ['rules' => []] + $sample), '404' => O::err('NotFound')] + $errs,
            ],
        ],
        '/security/access-list/status' => ['put' => [
            'operationId' => 'setServerAccessStatus', 'tags' => $T, 'summary' => 'Set what a refused visitor gets (server-wide)',
            'description' => $adm, 'x-permission' => 'security.manage',
            'requestBody' => O::body(['type' => 'object', 'required' => ['status'], 'properties' => ['status' => ['type' => 'integer', 'enum' => [403, 404, 444]]]], ['status' => 404]),
            'responses' => ['200' => O::res('The list.', $listRes, ['status' => 404] + $sample)] + $errs,
        ]],
        '/security/access-list/import' => ['post' => [
            'operationId' => 'importServerAccessList', 'tags' => $T, 'summary' => 'Import entries into the server-wide list',
            'description' => 'One entry per line, such as `deny ip 203.0.113.0/24  # scanner`. `mode`: `merge` (default) adds the entries and updates those that exist, `replace` makes the list exactly these entries. The list can have at most 1000 entries. ' . $adm,
            'x-permission' => 'security.manage',
            'requestBody' => O::body(['type' => 'object', 'required' => ['text'], 'properties' => ['text' => ['type' => 'string'], 'mode' => ['type' => 'string', 'enum' => ['merge', 'replace'], 'default' => 'merge']]], ['text' => "deny ip 203.0.113.0/24  # scanner\nallow ip 192.0.2.10", 'mode' => 'merge']),
            'responses' => ['200' => O::res('The list.', $listRes, $sample)] + $errs,
        ]],
        '/security/access-list/export' => ['get' => [
            'operationId' => 'exportServerAccessList', 'tags' => $T, 'summary' => 'Export the server-wide list as text',
            'description' => 'The list as text, one entry per line, in the format the import reads. ' . $adm,
            'x-permission' => 'security.view',
            'responses' => ['200' => O::res('The text.', ['type' => 'object', 'properties' => ['text' => ['type' => 'string']]], ['text' => "deny ip 203.0.113.0/24  # scanner\n"]), '403' => O::err('Forbidden'), '502' => O::err('AgentError')],
        ]],
    ],
];
