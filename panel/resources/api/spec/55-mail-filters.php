<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$filter = ['index' => 0, 'name' => 'Newsletters', 'enabled' => true, 'match' => 'any', 'rules' => [['part' => 'list-id', 'match' => 'contains', 'value' => 'news'], ['part' => 'from', 'match' => 'ends', 'value' => '@newsletter.example']], 'actions' => [['type' => 'fileinto', 'value' => 'Newsletters'], ['type' => 'mark_read', 'value' => '']]];
$responder = ['enabled' => true, 'subject' => 'Out of office', 'body' => "I am away until 5 December.\nFor urgent matters write to help@bluebird-cafe.example.", 'days' => 3, 'start' => '2026-11-27', 'end' => '2026-12-05'];
$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The email domain\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$mb = ['name' => 'mailbox', 'in' => 'path', 'required' => true, 'description' => 'The mailbox\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$n = ['name' => 'n', 'in' => 'path', 'required' => true, 'description' => 'The filter\'s `index` in the list (0 is the first). Filters run in this order and deleting one renumbers the ones after it, so read the list again before changing by index.', 'schema' => ['type' => 'integer', 'minimum' => 0]];
$fref = O::ref('MailFilter');
$filterProps = [
    'name' => ['type' => 'string', 'maxLength' => 100, 'examples' => ['Newsletters']],
    'enabled' => ['type' => 'boolean', 'default' => true],
    'match' => ['type' => 'string', 'enum' => ['all', 'any'], 'default' => 'all', 'description' => '`all`: every condition must match; `any`: one is enough.'],
    'rules' => ['type' => 'array', 'minItems' => 1, 'maxItems' => 20, 'items' => ['type' => 'object', 'required' => ['part', 'value'], 'properties' => [
        'part' => ['type' => 'string', 'enum' => array_keys(\NimVor\Services\MailFilterService::PARTS), 'description' => 'What to look at.'],
        'match' => ['type' => 'string', 'enum' => array_keys(\NimVor\Services\MailFilterService::MATCHES), 'default' => 'contains'],
        'value' => ['type' => 'string', 'maxLength' => 500],
    ]]],
    'actions' => ['type' => 'array', 'minItems' => 1, 'maxItems' => 20, 'items' => ['type' => 'object', 'required' => ['type'], 'properties' => [
        'type' => ['type' => 'string', 'enum' => array_keys(\NimVor\Services\MailFilterService::ACTIONS), 'description' => '`fileinto` moves to a folder, `redirect` forwards, `redirect_copy` keeps a copy and forwards, `mark_read`, `discard` deletes, `stop` skips the filters after this one.'],
        'value' => ['type' => 'string', 'maxLength' => 255, 'description' => 'The folder for `fileinto`; the address for `redirect` and `redirect_copy`. Not used by the others.'],
    ]]],
];

return [
    'tags' => [
        ['name' => 'Email filters', 'description' => "Mail filters and the auto-responder of a mailbox, and the routing of an email domain.\n\nFilters run on delivery, in list order. A filter has conditions (all of them or any of them) and actions. The whole set is written to the mail server in one go, so a change is all or nothing. A customer only reaches the mailboxes of their own email domains."],
    ],
    'schemas' => [
        'MailFilter' => [
            'type' => 'object',
            'properties' => ['index' => ['type' => 'integer', 'description' => 'Position in the list; use it in the path of the other filter endpoints.']] + $filterProps,
        ],
        'MailResponder' => [
            'type' => 'object',
            'properties' => [
                'enabled' => ['type' => 'boolean'],
                'subject' => ['type' => 'string', 'maxLength' => 200],
                'body' => ['type' => 'string', 'maxLength' => 10000],
                'days' => ['type' => 'integer', 'minimum' => 1, 'maximum' => 30, 'description' => 'Answer the same sender again only after this many days.'],
                'start' => ['type' => 'string', 'description' => '`YYYY-MM-DD`: start answering on this date; empty means now.'],
                'end' => ['type' => 'string', 'description' => '`YYYY-MM-DD`: stop after this date; empty means until turned off.'],
            ],
        ],
    ],
    'paths' => [
        '/mail/domains/{id}/mailboxes/{mailbox}/filters' => [
            'get' => [
                'operationId' => 'getMailboxFilters',
                'tags' => ['Email filters'],
                'summary' => 'Get a mailbox\'s filters and auto-responder',
                'x-permission' => 'mail.manage',
                'parameters' => [$id, $mb],
                'responses' => ['200' => O::res('The filters in the order they run, and the auto-responder.', ['type' => 'object', 'properties' => ['filters' => ['type' => 'array', 'items' => $fref], 'responder' => O::ref('MailResponder')]], ['filters' => [$filter], 'responder' => $responder]), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
            ],
            'post' => [
                'operationId' => 'addMailboxFilter',
                'tags' => ['Email filters'],
                'summary' => 'Add a filter',
                'description' => 'Adds the filter at the end of the list (a mailbox can have up to 50). Conditions and actions are checked before anything is sent to the mail server.',
                'x-permission' => 'mail.manage',
                'parameters' => [$id, $mb],
                'requestBody' => O::body(['type' => 'object', 'required' => ['name', 'rules', 'actions'], 'properties' => $filterProps], ['name' => 'Newsletters', 'match' => 'any', 'rules' => $filter['rules'], 'actions' => $filter['actions']]),
                'responses' => ['201' => O::res('The filter.', $fref, $filter), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/mail/domains/{id}/mailboxes/{mailbox}/filters/{n}' => [
            'put' => [
                'operationId' => 'replaceMailboxFilter',
                'tags' => ['Email filters'],
                'summary' => 'Replace a filter',
                'description' => 'Replaces the whole filter at this position (name, conditions and actions).',
                'x-permission' => 'mail.manage',
                'parameters' => [$id, $mb, $n],
                'requestBody' => O::body(['type' => 'object', 'required' => ['name', 'rules', 'actions'], 'properties' => $filterProps], ['name' => 'Newsletters', 'rules' => $filter['rules'], 'actions' => $filter['actions']]),
                'responses' => ['200' => O::res('The filter.', $fref, $filter), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
            'patch' => [
                'operationId' => 'toggleMailboxFilter',
                'tags' => ['Email filters'],
                'summary' => 'Turn a filter on or off',
                'description' => 'Only `enabled` can be changed this way; to change the conditions or actions replace the filter with `PUT`.',
                'x-permission' => 'mail.manage',
                'parameters' => [$id, $mb, $n],
                'requestBody' => O::body(['type' => 'object', 'required' => ['enabled'], 'properties' => ['enabled' => ['type' => 'boolean']]], ['enabled' => false]),
                'responses' => ['200' => O::res('The filter.', $fref, ['enabled' => false] + $filter), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
            'delete' => [
                'operationId' => 'deleteMailboxFilter',
                'tags' => ['Email filters'],
                'summary' => 'Delete a filter',
                'description' => 'The filters after it move up one position.',
                'x-permission' => 'mail.manage',
                'parameters' => [$id, $mb, $n],
                'responses' => ['200' => O::res('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'integer']]], ['deleted' => 0]), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
            ],
        ],
        '/mail/domains/{id}/mailboxes/{mailbox}/responder' => ['put' => [
            'operationId' => 'setMailboxResponder',
            'tags' => ['Email filters'],
            'summary' => 'Set the auto-responder',
            'description' => 'Replaces the auto-responder (vacation message). When `enabled` is true a subject and a message are required. The mailbox\'s filters are not changed.',
            'x-permission' => 'mail.manage',
            'parameters' => [$id, $mb],
            'requestBody' => O::body(['type' => 'object', 'required' => ['enabled'], 'properties' => [
                'enabled' => ['type' => 'boolean'],
                'subject' => ['type' => 'string', 'maxLength' => 200],
                'body' => ['type' => 'string', 'maxLength' => 10000],
                'days' => ['type' => 'integer', 'minimum' => 1, 'maximum' => 30, 'default' => 1],
                'start' => ['type' => 'string', 'description' => '`YYYY-MM-DD` or empty.'],
                'end' => ['type' => 'string', 'description' => '`YYYY-MM-DD` or empty; not before `start`.'],
            ]], $responder),
            'responses' => ['200' => O::res('The auto-responder.', O::ref('MailResponder'), $responder), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
        ]],
        '/mail/domains/{id}/routing' => ['put' => [
            'operationId' => 'setMailDomainRouting',
            'tags' => ['Email domains'],
            'summary' => 'Set where a domain\'s mail is delivered',
            'description' => '`local`: this server keeps the mail. `remote`: the domain\'s mail is handled by another server (this one only sends). `backup`: this server accepts mail when the main server is down and passes it on later. Remember to point the domain\'s MX records accordingly.',
            'x-permission' => 'mail.manage',
            'parameters' => [$id],
            'requestBody' => O::body(['type' => 'object', 'required' => ['routing'], 'properties' => ['routing' => ['type' => 'string', 'enum' => ['local', 'backup', 'remote']]]], ['routing' => 'local']),
            'responses' => ['200' => O::res('Changed.', ['type' => 'object', 'properties' => ['id' => ['type' => 'integer'], 'domain' => ['type' => 'string'], 'routing' => ['type' => 'string']]], ['id' => 5, 'domain' => 'bluebird-cafe.example', 'routing' => 'local']), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
        ]],
    ],
];
