<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$domain = ['id' => 5, 'domain' => 'bluebird-cafe.example', 'website_id' => 21, 'owner_id' => 12, 'routing' => 'local', 'dkim_selector' => 'mail', 'mailboxes' => 2, 'aliases' => 1, 'created_at' => '2026-11-23T19:50:00Z'];
$mailbox = ['id' => 9, 'local' => 'info', 'address' => 'info@bluebird-cafe.example', 'quota_mb' => 2048, 'created_at' => '2026-11-23T19:52:00Z'];
$alias = ['id' => 4, 'local' => 'sales', 'source' => 'sales@bluebird-cafe.example', 'catch_all' => false, 'destinations' => ['info@bluebird-cafe.example', 'owner@gmail.example'], 'created_at' => '2026-11-23T19:55:00Z'];
$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The email domain\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$mb = ['name' => 'mailbox', 'in' => 'path', 'required' => true, 'description' => 'The mailbox\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$al = ['name' => 'alias', 'in' => 'path', 'required' => true, 'description' => 'The alias\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$domainRef = O::ref('MailDomain');
$mailboxRef = O::ref('Mailbox');
$aliasRef = O::ref('MailAlias');
$dnsRecords = [['name' => '@', 'type' => 'MX', 'value' => 'mail.example.net.', 'priority' => 10, 'note' => 'Mail for the domain goes to this server.'], ['name' => '@', 'type' => 'TXT', 'value' => 'v=spf1 a mx ~all', 'priority' => 0, 'note' => 'SPF: this server may send for the domain.']];
$domainDetail = ['type' => 'object', 'allOf' => [$domainRef], 'properties' => ['dns_records' => ['type' => 'array', 'description' => 'The DNS records the domain needs for mail (MX, SPF, DKIM, DMARC). `POST /mail/domains/{id}/dns` adds the missing ones to a zone hosted here.', 'items' => ['type' => 'object', 'properties' => [
    'name' => ['type' => 'string'], 'type' => ['type' => 'string'], 'value' => ['type' => 'string'], 'priority' => ['type' => 'integer'], 'note' => ['type' => 'string'],
]]]]];

return [
    'tags' => [
        ['name' => 'Email domains', 'description' => "Domains this server receives and sends email for, with their DKIM keys and the DNS records they need.\n\nA hosting customer only reaches the email domains of their own websites; the plan's limits (mailboxes, quota per mailbox) apply as in the web interface."],
        ['name' => 'Mailboxes', 'description' => 'Mailboxes of an email domain. Passwords must be 10–128 characters (no colon) and meet the server\'s password policy; a generated password is returned once.'],
        ['name' => 'Email aliases', 'description' => 'Addresses that forward to one or more other addresses, including a catch-all for every other address of the domain.'],
    ],
    'schemas' => [
        'MailDomain' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer'],
                'domain' => ['type' => 'string'],
                'website_id' => ['type' => ['integer', 'null']],
                'owner_id' => ['type' => ['integer', 'null']],
                'routing' => ['type' => 'string', 'description' => 'Where the domain\'s mail is handled: `local` (this server), or the setting chosen in the web interface for mail hosted elsewhere.'],
                'dkim_selector' => ['type' => 'string'],
                'mailboxes' => ['type' => ['integer', 'null']],
                'aliases' => ['type' => ['integer', 'null']],
                'created_at' => ['type' => 'string', 'format' => 'date-time'],
            ],
        ],
        'Mailbox' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer'],
                'local' => ['type' => 'string', 'description' => 'The part before the @.'],
                'address' => ['type' => 'string', 'format' => 'email'],
                'quota_mb' => ['type' => 'integer', 'description' => 'Size limit in MB; 0 is unlimited (or the plan\'s cap).'],
                'created_at' => ['type' => 'string', 'format' => 'date-time'],
            ],
        ],
        'MailAlias' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer'],
                'local' => ['type' => 'string', 'description' => 'The part before the @; empty for the catch-all.'],
                'source' => ['type' => 'string'],
                'catch_all' => ['type' => 'boolean'],
                'destinations' => ['type' => 'array', 'items' => ['type' => 'string', 'format' => 'email']],
                'created_at' => ['type' => 'string', 'format' => 'date-time'],
            ],
        ],
    ],
    'paths' => [
        '/mail/domains' => [
            'get' => [
                'operationId' => 'listMailDomains',
                'tags' => ['Email domains'],
                'summary' => 'List email domains',
                'x-permission' => 'mail.manage',
                'responses' => ['200' => O::res('The email domains visible to the token.', ['type' => 'array', 'items' => $domainRef], [$domain])],
            ],
            'post' => [
                'operationId' => 'createMailDomain',
                'tags' => ['Email domains'],
                'summary' => 'Host email for a domain',
                'description' => 'Creates the domain on the mail server with its DKIM key. Customers can only do this for the domain of one of their own websites (`website_id`). Publish the returned `dns_records` (or call `POST /mail/domains/{id}/dns` when the zone is hosted here).',
                'x-permission' => 'mail.manage',
                'requestBody' => O::body([
                    'type' => 'object',
                    'properties' => [
                        'domain' => ['type' => 'string', 'description' => 'Administrators may host any domain; optional when `website_id` is given.', 'examples' => ['bluebird-cafe.example']],
                        'website_id' => ['type' => 'integer', 'description' => 'The website whose domain receives email.'],
                    ],
                ], ['website_id' => 21]),
                'responses' => ['201' => O::res('The email domain and the DNS records it needs.', $domainDetail, $domain + ['dns_records' => $dnsRecords]), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/mail/domains/{id}' => [
            'get' => [
                'operationId' => 'getMailDomain',
                'tags' => ['Email domains'],
                'summary' => 'Get an email domain',
                'x-permission' => 'mail.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The email domain and the DNS records it needs.', $domainDetail, $domain + ['dns_records' => $dnsRecords]), '404' => O::err('NotFound')],
            ],
            'delete' => [
                'operationId' => 'deleteMailDomain',
                'tags' => ['Email domains'],
                'summary' => 'Stop hosting email for a domain',
                'description' => '**Removes the domain\'s mailboxes and aliases from the mail server.** Give the domain name as `confirm`.',
                'x-permission' => 'mail.manage',
                'parameters' => [$id, O::query('confirm', 'The domain name, to confirm.', ['type' => 'string'], ['required' => true])],
                'responses' => ['200' => O::res('Removed.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'integer'], 'domain' => ['type' => 'string']]], ['deleted' => 5, 'domain' => 'bluebird-cafe.example']), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/mail/domains/{id}/dns' => ['post' => [
            'operationId' => 'publishMailDns',
            'tags' => ['Email domains'],
            'summary' => 'Add the mail DNS records to the domain\'s zone',
            'description' => 'Adds the missing MX, SPF, DKIM and DMARC records to the domain\'s zone on this server; records that are already there are left alone. Needs the DNS zone to exist here (`409` otherwise).',
            'x-permission' => 'mail.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('How many records were added.', ['type' => 'object', 'properties' => ['added' => ['type' => 'integer']]], ['added' => 3]), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
        '/mail/domains/{id}/dns-check' => ['get' => [
            'operationId' => 'checkMailDns',
            'tags' => ['Email domains'],
            'summary' => 'Check the domain\'s mail DNS records',
            'description' => 'Looks up MX, SPF, DKIM, DMARC and the server\'s reverse DNS as the internet sees them, and says what is missing or wrong.',
            'x-permission' => 'mail.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('One check per item.', ['type' => 'object', 'properties' => [
                'checks' => ['type' => 'array', 'items' => ['type' => 'object', 'description' => 'A check: its name, status (ok, warning, fail) and a message.']],
                'simulated' => ['type' => 'boolean', 'description' => 'True when the server could not look up DNS (a development setup).'],
            ]], ['checks' => [['name' => 'MX', 'status' => 'ok', 'message' => 'mail.example.net'], ['name' => 'SPF', 'status' => 'warning', 'message' => 'No SPF record found.']], 'simulated' => false]), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
        ]],
        '/mail/domains/{id}/mailboxes' => [
            'get' => [
                'operationId' => 'listMailboxes',
                'tags' => ['Mailboxes'],
                'summary' => 'List a domain\'s mailboxes',
                'x-permission' => 'mail.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The mailboxes, by name.', ['type' => 'array', 'items' => $mailboxRef], [$mailbox]), '404' => O::err('NotFound')],
            ],
            'post' => [
                'operationId' => 'createMailbox',
                'tags' => ['Mailboxes'],
                'summary' => 'Create a mailbox',
                'description' => 'Give a `password` or set `generate_password` (the password is returned once). Counts against the plan\'s mailbox limit and quota cap.',
                'x-permission' => 'mail.manage',
                'parameters' => [$id],
                'requestBody' => O::body([
                    'type' => 'object',
                    'required' => ['local'],
                    'properties' => [
                        'local' => ['type' => 'string', 'description' => 'The part before the @: letters, digits and . _ + - (not at the start or end).', 'examples' => ['info']],
                        'password' => ['type' => 'string', 'minLength' => 10, 'maxLength' => 128, 'description' => 'No colon. Omit when `generate_password` is true.'],
                        'generate_password' => ['type' => 'boolean', 'default' => false],
                        'quota_mb' => ['type' => 'integer', 'minimum' => 0, 'default' => 0, 'description' => '0: no limit (the plan\'s cap applies if it has one).'],
                    ],
                ], ['local' => 'info', 'generate_password' => true, 'quota_mb' => 2048]),
                'responses' => ['201' => O::res('The mailbox; `password` only when generated.', ['type' => 'object', 'properties' => ['mailbox' => $mailboxRef, 'password' => ['type' => ['string', 'null'], 'description' => 'Shown once.']]], ['mailbox' => $mailbox, 'password' => 'Qm7vR2x9…']), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/mail/domains/{id}/mailboxes/{mailbox}' => [
            'patch' => [
                'operationId' => 'updateMailbox',
                'tags' => ['Mailboxes'],
                'summary' => 'Change a mailbox',
                'description' => 'Change the quota and/or the password. Only what you send changes; the address cannot be renamed.',
                'x-permission' => 'mail.manage',
                'parameters' => [$id, $mb],
                'requestBody' => O::body(['type' => 'object', 'properties' => [
                    'quota_mb' => ['type' => 'integer', 'minimum' => 0],
                    'password' => ['type' => 'string', 'minLength' => 10, 'maxLength' => 128],
                    'generate_password' => ['type' => 'boolean', 'default' => false, 'description' => 'Set a new generated password and return it once.'],
                ]], ['quota_mb' => 4096]),
                'responses' => ['200' => O::res('The mailbox, changed.', ['type' => 'object', 'properties' => ['mailbox' => $mailboxRef, 'password' => ['type' => ['string', 'null']]]], ['mailbox' => ['quota_mb' => 4096] + $mailbox, 'password' => null]), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
            'delete' => [
                'operationId' => 'deleteMailbox',
                'tags' => ['Mailboxes'],
                'summary' => 'Delete a mailbox',
                'description' => 'Its mail is moved to the server\'s trash folder. Give the full address as `confirm`.',
                'x-permission' => 'mail.manage',
                'parameters' => [$id, $mb, O::query('confirm', 'The mailbox\'s address, to confirm.', ['type' => 'string'], ['required' => true])],
                'responses' => ['200' => O::res('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'integer'], 'address' => ['type' => 'string']]], ['deleted' => 9, 'address' => 'info@bluebird-cafe.example']), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/mail/domains/{id}/aliases' => [
            'get' => [
                'operationId' => 'listMailAliases',
                'tags' => ['Email aliases'],
                'summary' => 'List a domain\'s aliases',
                'x-permission' => 'mail.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The aliases; the catch-all (if any) first.', ['type' => 'array', 'items' => $aliasRef], [$alias]), '404' => O::err('NotFound')],
            ],
            'post' => [
                'operationId' => 'saveMailAlias',
                'tags' => ['Email aliases'],
                'summary' => 'Create or replace an alias',
                'description' => 'Forwards mail for an address of the domain to 1–50 addresses. If the alias exists its destinations are replaced. `local` `*` is the catch-all for every other address. A name that is a mailbox is refused.',
                'x-permission' => 'mail.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'required' => ['local', 'destinations'], 'properties' => [
                    'local' => ['type' => 'string', 'description' => 'The part before the @, or `*` for the catch-all.', 'examples' => ['sales']],
                    'destinations' => ['type' => 'array', 'minItems' => 1, 'maxItems' => 50, 'items' => ['type' => 'string', 'format' => 'email']],
                ]], ['local' => 'sales', 'destinations' => ['info@bluebird-cafe.example', 'owner@gmail.example']]),
                'responses' => ['201' => O::res('The alias.', $aliasRef, $alias), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/mail/domains/{id}/aliases/{alias}' => ['delete' => [
            'operationId' => 'deleteMailAlias',
            'tags' => ['Email aliases'],
            'summary' => 'Delete an alias',
            'x-permission' => 'mail.manage',
            'parameters' => [$id, $al],
            'responses' => ['200' => O::res('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'integer'], 'source' => ['type' => 'string']]], ['deleted' => 4, 'source' => 'sales@bluebird-cafe.example']), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
        ]],
    ],
];
