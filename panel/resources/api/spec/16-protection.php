<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$limit = ['name' => 'login', 'match' => 'exact', 'path' => '/wp-login.php', 'rate' => 5, 'unit' => 'm', 'burst' => 2, 'per_ua' => false, 'enabled' => true];
$rule = ['kind' => 'ip', 'action' => 'deny', 'value' => '203.0.113.0/24', 'note' => 'scanner'];
$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The website\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$limitRef = O::ref('TrafficLimit');
$limitsRes = ['type' => 'object', 'properties' => ['status' => ['type' => 'integer', 'enum' => [429, 503, 444], 'description' => 'What a visitor over a limit gets: 429, 503, or 444 (the connection is closed without an answer).'], 'limits' => ['type' => 'array', 'items' => $limitRef]]];
$listRes = ['type' => 'object', 'properties' => [
    'status' => ['type' => 'integer', 'enum' => [403, 404, 444], 'description' => 'What a refused visitor gets: 403, 404, or 444 (the connection is closed without an answer).'],
    'max' => ['type' => 'integer', 'description' => 'The most entries a website can have.'],
    'rules' => ['type' => 'array', 'items' => O::ref('AccessRule')],
]];
$sample = ['status' => 403, 'max' => 200, 'rules' => [$rule]];

return [
    'tags' => [
        ['name' => 'Web protection', 'description' => "Per-website protection against abusive traffic: request-rate limits, an allow/deny list and a country restriction.\n\nEach change is written to the website's web server configuration, checked and reloaded; if the server refuses it the change is undone. A suspended account's protection cannot be changed (`409`). The server-wide lists and the ModSecurity firewall are administrator tools and are not part of this API yet."],
    ],
    'schemas' => [
        'TrafficLimit' => [
            'type' => 'object',
            'properties' => [
                'name' => ['type' => 'string', 'pattern' => '^[a-z0-9][a-z0-9_-]{0,39}$', 'description' => 'Identifies the limit; sending a limit with an existing name replaces it.', 'examples' => ['login']],
                'match' => ['type' => 'string', 'enum' => ['prefix', 'exact'], 'default' => 'prefix', 'description' => '`prefix`: the path and everything below it; `exact`: that one address.'],
                'path' => ['type' => 'string', 'description' => 'A URL path: `/` (the whole website), `/api/`, `/wp-login.php`.'],
                'rate' => ['type' => 'integer', 'minimum' => 1, 'maximum' => 60000, 'description' => 'Requests allowed from one address: up to 1000 per second or 60000 per minute.'],
                'unit' => ['type' => 'string', 'enum' => ['s', 'm'], 'default' => 's', 'description' => '`s` per second, `m` per minute.'],
                'burst' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 1000, 'default' => 0, 'description' => 'Extra requests tolerated in a burst before the limit bites.'],
                'per_ua' => ['type' => 'boolean', 'default' => false, 'description' => 'Count each browser (User-Agent) of an address separately.'],
                'enabled' => ['type' => 'boolean', 'description' => 'False keeps the limit but does not apply it.'],
            ],
        ],
        'CountryRestriction' => [
            'type' => 'object',
            'properties' => [
                'mode' => ['type' => 'string', 'enum' => ['off', 'deny', 'allow_only']],
                'codes' => ['type' => 'array', 'items' => ['type' => 'string']],
                'refusal_status' => ['type' => 'integer', 'description' => 'What a refused visitor gets (shared with the allow/deny list).'],
                'database_loaded' => ['type' => 'boolean', 'description' => 'Whether the country address database is loaded. Without it a restriction cannot be set.'],
            ],
        ],
        'AccessRule' => [
            'type' => 'object',
            'properties' => [
                'kind' => ['type' => 'string', 'enum' => ['ip', 'ua', 'url'], 'description' => '`ip` an address or network, `ua` part of the browser\'s User-Agent, `url` a path.'],
                'action' => ['type' => 'string', 'enum' => ['deny', 'allow']],
                'value' => ['type' => 'string', 'examples' => ['203.0.113.0/24']],
                'note' => ['type' => 'string', 'description' => 'Your remark about the entry.'],
            ],
        ],
    ],
    'paths' => [
        '/websites/{id}/traffic-limits' => [
            'get' => [
                'operationId' => 'listTrafficLimits',
                'tags' => ['Web protection'],
                'summary' => 'List a website\'s request-rate limits',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The limits and what an over-limit visitor gets.', $limitsRes, ['status' => 429, 'limits' => [$limit]]), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
            ],
            'post' => [
                'operationId' => 'saveTrafficLimit',
                'tags' => ['Web protection'],
                'summary' => 'Add or replace a request-rate limit',
                'description' => 'Visitors that make more than `rate` requests per `unit` from one address to the path get an error. A limit with the same `name` is replaced (`200`), a new one is added (`201`); a website can have at most 20.',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'required' => ['name', 'path', 'rate'], 'properties' => [
                    'name' => ['type' => 'string', 'pattern' => '^[a-z0-9][a-z0-9_-]{0,39}$'], 'match' => ['type' => 'string', 'enum' => ['prefix', 'exact'], 'default' => 'prefix'], 'path' => ['type' => 'string'], 'rate' => ['type' => 'integer', 'minimum' => 1],
                    'unit' => ['type' => 'string', 'enum' => ['s', 'm'], 'default' => 's'], 'burst' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 1000], 'per_ua' => ['type' => 'boolean'],
                ]], ['name' => 'login', 'match' => 'exact', 'path' => '/wp-login.php', 'rate' => 5, 'unit' => 'm', 'burst' => 2]),
                'responses' => ['201' => O::res('The limit (added).', $limitRef, $limit), '200' => O::res('The limit (replaced).', $limitRef, $limit), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/traffic-limits/status' => ['put' => [
            'operationId' => 'setTrafficLimitStatus',
            'tags' => ['Web protection'],
            'summary' => 'Set what an over-limit visitor gets',
            'description' => 'The same answer for all of the website\'s limits.',
            'x-permission' => 'websites.manage',
            'parameters' => [$id],
            'requestBody' => O::body(['type' => 'object', 'required' => ['status'], 'properties' => ['status' => ['type' => 'integer', 'enum' => [429, 503, 444]]]], ['status' => 429]),
            'responses' => ['200' => O::res('The limits.', $limitsRes, ['status' => 429, 'limits' => [$limit]]), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/traffic-limits/{name}' => [
            'patch' => [
                'operationId' => 'toggleTrafficLimit',
                'tags' => ['Web protection'],
                'summary' => 'Turn a limit on or off',
                'x-permission' => 'websites.manage',
                'parameters' => [$id, ['name' => 'name', 'in' => 'path', 'required' => true, 'description' => 'The limit\'s name.', 'schema' => ['type' => 'string']]],
                'requestBody' => O::body(['type' => 'object', 'required' => ['enabled'], 'properties' => ['enabled' => ['type' => 'boolean']]], ['enabled' => false]),
                'responses' => ['200' => O::res('The limit.', $limitRef, ['enabled' => false] + $limit), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
            'delete' => [
                'operationId' => 'deleteTrafficLimit',
                'tags' => ['Web protection'],
                'summary' => 'Delete a limit',
                'x-permission' => 'websites.manage',
                'parameters' => [$id, ['name' => 'name', 'in' => 'path', 'required' => true, 'description' => 'The limit\'s name.', 'schema' => ['type' => 'string']]],
                'responses' => ['200' => O::res('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'string']]], ['deleted' => 'login']), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/access-list' => [
            'get' => [
                'operationId' => 'getAccessList',
                'tags' => ['Web protection'],
                'summary' => 'Get a website\'s allow/deny list',
                'description' => 'Entries by address, browser or URL. An `allow` entry lets that visitor through even when another entry or the country restriction would refuse them.',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The list.', $listRes, $sample), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
            ],
            'post' => [
                'operationId' => 'addAccessRule',
                'tags' => ['Web protection'],
                'summary' => 'Add an entry',
                'description' => 'Adds the entry, or changes the action and note of the entry with the same kind and value. Returns the whole list.',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'required' => ['kind', 'action', 'value'], 'properties' => ['kind' => ['type' => 'string', 'enum' => ['ip', 'ua', 'url']], 'action' => ['type' => 'string', 'enum' => ['deny', 'allow']], 'value' => ['type' => 'string'], 'note' => ['type' => 'string', 'maxLength' => 200]]], $rule),
                'responses' => ['201' => O::res('The list.', $listRes, $sample), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
            'delete' => [
                'operationId' => 'deleteAccessRule',
                'tags' => ['Web protection'],
                'summary' => 'Delete an entry',
                'x-permission' => 'websites.manage',
                'parameters' => [$id, ['name' => 'kind', 'in' => 'query', 'required' => true, 'description' => '`ip`, `ua` or `url`.', 'schema' => ['type' => 'string', 'enum' => ['ip', 'ua', 'url']]], ['name' => 'value', 'in' => 'query', 'required' => true, 'description' => 'The entry\'s value, as the list shows it (URL-encode it).', 'schema' => ['type' => 'string'], 'example' => '203.0.113.0/24']],
                'responses' => ['200' => O::res('The list, without the entry.', $listRes, ['rules' => []] + $sample), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/access-list/status' => ['put' => [
            'operationId' => 'setAccessListStatus',
            'tags' => ['Web protection'],
            'summary' => 'Set what a refused visitor gets',
            'x-permission' => 'websites.manage',
            'parameters' => [$id],
            'requestBody' => O::body(['type' => 'object', 'required' => ['status'], 'properties' => ['status' => ['type' => 'integer', 'enum' => [403, 404, 444]]]], ['status' => 403]),
            'responses' => ['200' => O::res('The list.', $listRes, $sample), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/access-list/import' => ['post' => [
            'operationId' => 'importAccessList',
            'tags' => ['Web protection'],
            'summary' => 'Import entries from text',
            'description' => 'One entry per line, in the form `deny ip 203.0.113.0/24  # note`. `merge` adds to the list (an entry with the same kind and value is changed); `replace` makes the text the whole list.',
            'x-permission' => 'websites.manage',
            'parameters' => [$id],
            'requestBody' => O::body(['type' => 'object', 'required' => ['text'], 'properties' => ['text' => ['type' => 'string'], 'mode' => ['type' => 'string', 'enum' => ['merge', 'replace'], 'default' => 'merge']]], ['text' => "deny ip 203.0.113.0/24  # scanner\ndeny ua BadBot", 'mode' => 'merge']),
            'responses' => ['200' => O::res('The list.', $listRes, $sample), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
        ]],
        '/websites/{id}/countries' => [
            'get' => [
                'operationId' => 'getCountryRestriction',
                'tags' => ['Web protection'],
                'summary' => 'Get a website\'s country restriction',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The restriction.', O::ref('CountryRestriction'), ['mode' => 'deny', 'codes' => ['RU', 'KP'], 'refusal_status' => 403, 'database_loaded' => true]), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
            ],
            'put' => [
                'operationId' => 'setCountryRestriction',
                'tags' => ['Web protection'],
                'summary' => 'Set the country restriction',
                'description' => 'Refuses the selected countries (`deny`), serves only them (`allow_only`) or turns the restriction off (`off`). It needs the country address database, which an administrator loads; without it the answer is `409`. Entries of the allow/deny list with `allow` still get through. **`allow_only` with the wrong countries locks real visitors out.**',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'required' => ['mode'], 'properties' => ['mode' => ['type' => 'string', 'enum' => ['off', 'deny', 'allow_only']], 'codes' => ['type' => 'array', 'items' => ['type' => 'string', 'minLength' => 2, 'maxLength' => 2], 'description' => 'ISO two-letter country codes; required unless `mode` is `off`.', 'examples' => [['RU', 'KP']]]]], ['mode' => 'deny', 'codes' => ['RU', 'KP']]),
                'responses' => ['200' => O::res('The restriction.', O::ref('CountryRestriction'), ['mode' => 'deny', 'codes' => ['RU', 'KP'], 'refusal_status' => 403, 'database_loaded' => true]), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
    ],
];
