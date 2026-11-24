<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$child = ['id' => 24, 'domain' => 'blog.bluebird-cafe.example', 'aliases' => [], 'kind' => 'php', 'document_root' => '/home/nimvor/sites/blog.bluebird-cafe.example/public_html', 'https' => false, 'enabled' => true, 'created_at' => '2026-11-24T08:00:00Z'];
$redirect = ['from' => '/old-menu', 'to' => 'https://bluebird-cafe.example/menu', 'code' => 301];
$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The website\'s id (the parent).', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$ref = O::ref('ChildWebsite');
$created = ['type' => 'object', 'allOf' => [$ref], 'properties' => ['dns_record_added' => ['type' => 'boolean', 'description' => 'For a subdomain created with `dns: true`: an `A` record was added to the parent\'s DNS zone. False when there is no zone or the record already existed (add it on the DNS endpoints).']]];

return [
    'tags' => [
        ['name' => 'Subdomains and redirects', 'description' => "Subdomains and addon domains of a website, and its redirects.\n\nA subdomain (`blog.example.com`) or an addon domain (a different domain) is a website of its own, with its own document root, running as the same system account as its parent. They show in `GET /websites` too, and removing one is `DELETE /websites/{id}` with its id. Plan limits for subdomains, addon domains and websites apply. The web server configuration is checked before it is reloaded and a refused change is undone.\n\nRedirects are exact-path redirects of one website (up to 100)."],
    ],
    'schemas' => [
        'ChildWebsite' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer', 'description' => 'The website\'s id; use it with the `/websites/{id}` endpoints.'],
                'domain' => ['type' => 'string'], 'aliases' => ['type' => 'array', 'items' => ['type' => 'string']],
                'kind' => ['type' => 'string', 'enum' => ['php', 'static', 'proxy']],
                'document_root' => ['type' => 'string'], 'https' => ['type' => 'boolean'], 'enabled' => ['type' => 'boolean'],
                'created_at' => ['type' => 'string', 'format' => 'date-time'],
            ],
        ],
        'Redirect' => [
            'type' => 'object',
            'properties' => [
                'from' => ['type' => 'string', 'description' => 'The path to redirect, starting with `/` (letters, digits and `. _ ~ - /`).', 'examples' => ['/old-menu']],
                'to' => ['type' => 'string', 'description' => 'A full address (`https://example.com/new`) or a path (`/new`).'],
                'code' => ['type' => 'integer', 'enum' => [301, 302], 'default' => 301, 'description' => '`301` permanent, `302` temporary.'],
            ],
        ],
    ],
    'paths' => [
        '/websites/{id}/subdomains' => [
            'get' => [
                'operationId' => 'listSubdomains',
                'tags' => ['Subdomains and redirects'],
                'summary' => 'List a website\'s subdomains',
                'x-permission' => 'websites.view',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The subdomains.', ['type' => 'array', 'items' => $ref], [$child]), '404' => O::err('NotFound')],
            ],
            'post' => [
                'operationId' => 'createSubdomain',
                'tags' => ['Subdomains and redirects'],
                'summary' => 'Create a subdomain',
                'description' => 'Creates `<subdomain>.<website domain>` as a website of the same type and PHP version as its parent. With `dns: true` an `A` record for it is added to the parent\'s DNS zone (when the parent has one and the server\'s default address is set).',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'required' => ['subdomain'], 'properties' => [
                    'subdomain' => ['type' => 'string', 'description' => 'The label before the parent domain; `www` is not allowed (it is an alias of the website itself).', 'examples' => ['blog']],
                    'dns' => ['type' => 'boolean', 'default' => false, 'description' => 'Also add an `A` record to the parent\'s DNS zone.'],
                ]], ['subdomain' => 'blog', 'dns' => true]),
                'responses' => ['201' => O::res('The subdomain.', $created, $child + ['dns_record_added' => true]), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/addons' => [
            'get' => [
                'operationId' => 'listAddonDomains',
                'tags' => ['Subdomains and redirects'],
                'summary' => 'List a website\'s addon domains',
                'x-permission' => 'websites.view',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The addon domains.', ['type' => 'array', 'items' => $ref], [['domain' => 'harbor-books.example', 'document_root' => '/home/nimvor/sites/harbor-books.example/public_html'] + $child]), '404' => O::err('NotFound')],
            ],
            'post' => [
                'operationId' => 'createAddonDomain',
                'tags' => ['Subdomains and redirects'],
                'summary' => 'Add an addon domain',
                'description' => 'Hosts another domain as a website of its own under the same system account. The domain must not be a subdomain of the parent (create it as a subdomain). Point the domain\'s DNS to this server separately.',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'required' => ['domain'], 'properties' => [
                    'domain' => ['type' => 'string', 'examples' => ['harbor-books.example']],
                    'www' => ['type' => 'boolean', 'default' => false, 'description' => 'Also serve `www.<domain>`.'],
                ]], ['domain' => 'harbor-books.example', 'www' => true]),
                'responses' => ['201' => O::res('The addon domain.', $ref, ['domain' => 'harbor-books.example', 'aliases' => ['www.harbor-books.example']] + $child), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/websites/{id}/redirects' => [
            'get' => [
                'operationId' => 'listRedirects',
                'tags' => ['Subdomains and redirects'],
                'summary' => 'List a website\'s redirects',
                'x-permission' => 'websites.view',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The redirects.', ['type' => 'array', 'items' => O::ref('Redirect')], [$redirect]), '404' => O::err('NotFound')],
            ],
            'post' => [
                'operationId' => 'addRedirect',
                'tags' => ['Subdomains and redirects'],
                'summary' => 'Add a redirect',
                'description' => 'Redirects one exact path. A path can have one redirect (delete it first to replace it) and a page cannot redirect to itself.',
                'x-permission' => 'websites.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'required' => ['from', 'to'], 'properties' => ['from' => ['type' => 'string'], 'to' => ['type' => 'string'], 'code' => ['type' => 'integer', 'enum' => [301, 302], 'default' => 301]]], $redirect),
                'responses' => ['201' => O::res('The redirect.', O::ref('Redirect'), $redirect), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
            'delete' => [
                'operationId' => 'deleteRedirect',
                'tags' => ['Subdomains and redirects'],
                'summary' => 'Delete a redirect',
                'x-permission' => 'websites.manage',
                'parameters' => [$id, ['name' => 'from', 'in' => 'query', 'required' => true, 'description' => 'The redirect\'s path, exactly as the list shows it (URL-encode it).', 'schema' => ['type' => 'string'], 'example' => '/old-menu']],
                'responses' => ['200' => O::res('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'string']]], ['deleted' => '/old-menu']), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
    ],
];
