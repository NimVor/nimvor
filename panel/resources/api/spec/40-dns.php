<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$zone = [
    'id' => 3, 'name' => 'bluebird-cafe.example', 'website_id' => 21, 'owner_id' => 12, 'ttl' => 3600, 'primary_ns' => 'ns1.example.net',
    'hostmaster' => 'hostmaster@bluebird-cafe.example', 'secondaries' => [], 'serial' => 2026112301, 'loaded_at' => '2026-11-23T19:45:00Z',
    'records' => 9, 'created_at' => '2026-11-23T19:44:00Z', 'updated_at' => '2026-11-23T19:45:00Z',
];
$record = ['id' => 41, 'name' => 'www', 'type' => 'CNAME', 'ttl' => 0, 'value' => '@', 'priority' => 0, 'weight' => 0, 'port' => 0, 'disabled' => false];
$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The zone\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$rec = ['name' => 'record', 'in' => 'path', 'required' => true, 'description' => 'The record\'s id (from `GET /dns/zones/{id}/records`).', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$zoneRef = O::ref('DnsZone');
$recRef = O::ref('DnsRecord');
$zoneDetail = ['type' => 'object', 'allOf' => [$zoneRef], 'properties' => ['warnings' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'Problems the zone has (for example a missing address for a name server); an empty list when it looks fine.']]];

$recordProps = [
    'name' => ['type' => 'string', 'description' => '`@` for the zone itself, or a name such as `www`, `mail`, `*.dev` (relative to the zone).', 'examples' => ['www']],
    'type' => ['type' => 'string', 'enum' => ['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'NS', 'SRV', 'CAA', 'PTR']],
    'value' => ['type' => 'string', 'description' => 'By type: an IPv4 address (A), an IPv6 address (AAAA), a host name (CNAME, NS, PTR, MX target, SRV target; a name that does not end in a dot is relative to the zone, `@` is the zone itself), text (TXT; one line, up to 4000 characters), `<flag> <tag> <value>` or `<tag> <value>` (CAA, for example `issue letsencrypt.org`).'],
    'ttl' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 604800, 'default' => 0, 'description' => 'Seconds, 60–604800; 0 uses the zone\'s default.'],
    'priority' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 65535, 'description' => 'MX and SRV: lower is preferred.'],
    'weight' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 65535, 'description' => 'SRV only.'],
    'port' => ['type' => 'integer', 'minimum' => 0, 'maximum' => 65535, 'description' => 'SRV only.'],
];

return [
    'tags' => [
        ['name' => 'DNS zones', 'description' => "DNS zones this server answers for, with their settings, zone files and DNSSEC.\n\nEvery change is checked and loaded by the DNS server; if the server refuses the zone, nothing changes. A hosting customer only sees the zones of their own websites and can only create the zone of a website of theirs."],
        ['name' => 'DNS records', 'description' => 'The records of a zone. Each type has its own checks (IPv4/IPv6 syntax, host names, MX priority, SRV and CAA fields, TTL), a CNAME cannot sit next to other records of the same name, and a zone holds at most 2000 records.'],
    ],
    'schemas' => [
        'DnsZone' => [
            'type' => 'object',
            'properties' => [
                'id' => ['type' => 'integer'],
                'name' => ['type' => 'string', 'description' => 'The domain, for example `example.com`.'],
                'website_id' => ['type' => ['integer', 'null'], 'description' => 'The website this zone belongs to.'],
                'owner_id' => ['type' => ['integer', 'null'], 'description' => 'The panel user who owns it.'],
                'ttl' => ['type' => 'integer', 'description' => 'Default TTL in seconds.'],
                'primary_ns' => ['type' => 'string'],
                'hostmaster' => ['type' => 'string'],
                'secondaries' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'IP addresses of secondary servers allowed to transfer the zone.'],
                'serial' => ['type' => 'integer', 'description' => 'The zone\'s serial number.'],
                'loaded_at' => ['type' => ['string', 'null'], 'format' => 'date-time', 'description' => 'When the DNS server last loaded the zone.'],
                'records' => ['type' => ['integer', 'null'], 'description' => 'How many records it has.'],
                'created_at' => ['type' => 'string', 'format' => 'date-time'],
                'updated_at' => ['type' => 'string', 'format' => 'date-time'],
            ],
        ],
        'DnsSecStatus' => [
            'type' => 'object',
            'properties' => [
                'supported' => ['type' => 'boolean', 'description' => 'False when the DNS server in use cannot sign zones.'],
                'reason' => ['type' => 'string', 'description' => 'Why signing is not supported.'],
                'backend' => ['type' => 'string'],
                'backend_label' => ['type' => 'string'],
                'enabled' => ['type' => 'boolean', 'description' => 'The zone is signed.'],
                'ds' => ['type' => 'array', 'items' => ['type' => 'object'], 'description' => 'The DS records to publish at the registrar.'],
                'ds_error' => ['type' => 'string'],
                'warning' => ['type' => 'string', 'description' => 'Only after enabling: something to know.'],
            ],
        ],
        'DnsRecord' => [
            'type' => 'object',
            'properties' => ['id' => ['type' => 'integer']] + $recordProps + ['disabled' => ['type' => 'boolean', 'description' => 'A disabled record stays in the panel but is not served.']],
        ],
    ],
    'paths' => [
        '/dns/zones' => [
            'get' => [
                'operationId' => 'listDnsZones',
                'tags' => ['DNS zones'],
                'summary' => 'List DNS zones',
                'x-permission' => 'dns.manage',
                'parameters' => [['$ref' => '#/components/parameters/Server']],
                'responses' => ['200' => O::res('The zones visible to the token, by name.', ['type' => 'array', 'items' => $zoneRef], [$zone])],
            ],
            'post' => [
                'operationId' => 'createDnsZone',
                'tags' => ['DNS zones'],
                'summary' => 'Create a DNS zone',
                'description' => 'The zone gets the name servers set under Settings → DNS (an administrator sets them). With `template` it also gets the usual records: the address and `www`, SPF, optionally DMARC, and a CAA record for Let\'s Encrypt. Customers can only create the zone of one of their own websites (`website_id`), under the website\'s own domain.',
                'x-permission' => 'dns.manage',
                'parameters' => [['$ref' => '#/components/parameters/Server']],
                'requestBody' => O::body([
                    'type' => 'object',
                    'properties' => [
                        'name' => ['type' => 'string', 'description' => 'The domain. Optional when `website_id` is given (the website\'s domain is used).', 'examples' => ['bluebird-cafe.example']],
                        'website_id' => ['type' => 'integer', 'description' => 'The website the zone belongs to.'],
                        'template' => ['type' => 'boolean', 'default' => false, 'description' => 'Add the usual records (address, `www`, SPF, CAA).'],
                        'spf' => ['type' => 'boolean', 'default' => true, 'description' => 'With `template`: add an SPF record.'],
                        'dmarc' => ['type' => 'boolean', 'default' => false, 'description' => 'With `template`: add a DMARC record.'],
                    ],
                ], ['name' => 'bluebird-cafe.example', 'website_id' => 21, 'template' => true]),
                'responses' => [
                    '201' => O::res('The zone, loaded.', $zoneDetail, $zone + ['warnings' => []]),
                    '409' => O::err('Conflict'),
                    '422' => O::err('ValidationFailed'),
                    '502' => O::err('AgentError'),
                ],
            ],
        ],
        '/dns/zones/{id}' => [
            'get' => [
                'operationId' => 'getDnsZone',
                'tags' => ['DNS zones'],
                'summary' => 'Get a DNS zone',
                'x-permission' => 'dns.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The zone, with any warnings about it.', $zoneDetail, $zone + ['warnings' => []]), '404' => O::err('NotFound')],
            ],
            'patch' => [
                'operationId' => 'updateDnsZone',
                'tags' => ['DNS zones'],
                'summary' => 'Change a zone\'s settings',
                'description' => 'The default TTL and the secondary servers allowed to transfer the zone. Only the fields you send change.',
                'x-permission' => 'dns.manage',
                'parameters' => [$id],
                'requestBody' => O::body([
                    'type' => 'object',
                    'properties' => [
                        'ttl' => ['type' => 'integer', 'minimum' => 60, 'maximum' => 604800],
                        'secondaries' => ['type' => 'array', 'items' => ['type' => 'string'], 'description' => 'IP addresses; an empty list allows none.'],
                    ],
                ], ['ttl' => 1800, 'secondaries' => ['203.0.113.53']]),
                'responses' => ['200' => O::res('The zone, changed and reloaded.', $zoneDetail, ['ttl' => 1800, 'secondaries' => ['203.0.113.53'], 'warnings' => []] + $zone), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
            'delete' => [
                'operationId' => 'deleteDnsZone',
                'tags' => ['DNS zones'],
                'summary' => 'Delete a DNS zone',
                'description' => '**The domain stops resolving from this server.** Give the zone\'s name as `confirm`.',
                'x-permission' => 'dns.manage',
                'parameters' => [$id, O::query('confirm', 'The zone\'s name, to confirm.', ['type' => 'string'], ['required' => true])],
                'responses' => ['200' => O::res('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'integer'], 'name' => ['type' => 'string']]], ['deleted' => 3, 'name' => 'bluebird-cafe.example']), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/dns/zones/{id}/sync' => ['post' => [
            'operationId' => 'syncDnsZone',
            'tags' => ['DNS zones'],
            'summary' => 'Load the zone again',
            'description' => 'Sends the whole zone to the DNS server again (for example after the DNS server was reinstalled).',
            'x-permission' => 'dns.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('The zone, loaded.', $zoneDetail, $zone + ['warnings' => []]), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
        ]],
        '/dns/zones/{id}/export' => ['get' => [
            'operationId' => 'exportDnsZone',
            'tags' => ['DNS zones'],
            'summary' => 'Export the zone file',
            'description' => 'The zone as the DNS server serves it, in BIND zone file format.',
            'x-permission' => 'dns.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('The zone file.', ['type' => 'object', 'properties' => ['zone' => ['type' => 'string'], 'zone_file' => ['type' => 'string', 'description' => 'BIND zone file text.']]], ['zone' => 'bluebird-cafe.example', 'zone_file' => "\$TTL 3600\n@ IN SOA ns1.example.net. hostmaster.bluebird-cafe.example. (…)\n"]), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
        ]],
        '/dns/zones/{id}/import' => ['post' => [
            'operationId' => 'importDnsZone',
            'tags' => ['DNS zones'],
            'summary' => 'Import a zone file',
            'description' => 'Adds the records of a BIND zone file that are not in the zone yet. The SOA record, lines that are not supported, and records the zone could not hold (a CNAME next to other records) are skipped and listed in `skipped`; the zone\'s own name servers stay as they are. Up to 1 MB.',
            'x-permission' => 'dns.manage',
            'parameters' => [$id],
            'requestBody' => O::body(['type' => 'object', 'required' => ['zone_file'], 'properties' => ['zone_file' => ['type' => 'string', 'description' => 'The zone file text.']]], ['zone_file' => "www 3600 IN A 192.0.2.10\nmail 3600 IN A 192.0.2.11\n"]),
            'responses' => ['200' => O::res('What was imported.', ['type' => 'object', 'properties' => [
                'added' => ['type' => 'integer'], 'duplicates' => ['type' => 'integer', 'description' => 'Records already in the zone.'],
                'skipped' => ['type' => 'array', 'items' => ['type' => 'object', 'properties' => ['line' => ['type' => 'integer'], 'text' => ['type' => 'string'], 'reason' => ['type' => 'string']]]],
            ]], ['added' => 2, 'duplicates' => 0, 'skipped' => []]), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
        ]],
        '/dns/zones/{id}/records' => [
            'get' => [
                'operationId' => 'listDnsRecords',
                'tags' => ['DNS records'],
                'summary' => 'List a zone\'s records',
                'x-permission' => 'dns.manage',
                'parameters' => [$id],
                'responses' => ['200' => O::res('The records: the zone\'s `@` records first, then by name and type.', ['type' => 'array', 'items' => $recRef], [$record]), '404' => O::err('NotFound')],
            ],
            'post' => [
                'operationId' => 'createDnsRecord',
                'tags' => ['DNS records'],
                'summary' => 'Add a record',
                'description' => 'Checked for its type, added, and the zone reloaded; nothing changes when the DNS server refuses the zone.',
                'x-permission' => 'dns.manage',
                'parameters' => [$id],
                'requestBody' => O::body(['type' => 'object', 'required' => ['type', 'value'], 'properties' => $recordProps], ['name' => 'mail', 'type' => 'A', 'value' => '192.0.2.11', 'ttl' => 3600]),
                'responses' => ['201' => O::res('The record.', $recRef, ['id' => 42, 'name' => 'mail', 'type' => 'A', 'ttl' => 3600, 'value' => '192.0.2.11', 'priority' => 0, 'weight' => 0, 'port' => 0, 'disabled' => false]), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
        ],
        '/dns/zones/{id}/records/{record}' => [
            'put' => [
                'operationId' => 'updateDnsRecord',
                'tags' => ['DNS records'],
                'summary' => 'Replace a record',
                'description' => 'Send the complete record (name, type, value and the fields its type needs); it replaces the stored one.',
                'x-permission' => 'dns.manage',
                'parameters' => [$id, $rec],
                'requestBody' => O::body(['type' => 'object', 'required' => ['type', 'value'], 'properties' => $recordProps], ['name' => 'mail', 'type' => 'A', 'value' => '192.0.2.99', 'ttl' => 300]),
                'responses' => ['200' => O::res('The record, changed.', $recRef, ['id' => 42, 'name' => 'mail', 'type' => 'A', 'ttl' => 300, 'value' => '192.0.2.99', 'priority' => 0, 'weight' => 0, 'port' => 0, 'disabled' => false]), '404' => O::err('NotFound'), '422' => O::err('ValidationFailed'), '502' => O::err('AgentError')],
            ],
            'delete' => [
                'operationId' => 'deleteDnsRecord',
                'tags' => ['DNS records'],
                'summary' => 'Delete a record',
                'x-permission' => 'dns.manage',
                'parameters' => [$id, $rec],
                'responses' => ['200' => O::res('Deleted.', ['type' => 'object', 'properties' => ['deleted' => ['type' => 'integer']]], ['deleted' => 42]), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
            ],
        ],
        '/dns/zones/{id}/records/bulk' => ['post' => [
            'operationId' => 'bulkDnsRecords',
            'tags' => ['DNS records'],
            'summary' => 'Turn records on or off, or delete several',
            'description' => 'Applies `op` to the listed records of this zone in one reload. `disable` keeps a record in the panel but stops serving it.',
            'x-permission' => 'dns.manage',
            'parameters' => [$id],
            'requestBody' => O::body(['type' => 'object', 'required' => ['op', 'ids'], 'properties' => [
                'op' => ['type' => 'string', 'enum' => ['enable', 'disable', 'delete']],
                'ids' => ['type' => 'array', 'items' => ['type' => 'integer'], 'description' => 'Record ids of this zone.'],
            ]], ['op' => 'disable', 'ids' => [41, 42]]),
            'responses' => ['200' => O::res('How many records were changed.', ['type' => 'object', 'properties' => ['changed' => ['type' => 'integer'], 'op' => ['type' => 'string']]], ['changed' => 2, 'op' => 'disable']), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
        '/dns/zones/{id}/dnssec' => ['get' => [
            'operationId' => 'getDnsSecStatus',
            'tags' => ['DNS zones'],
            'summary' => 'DNSSEC status and DS records',
            'description' => 'Administrators only. Whether the DNS server can sign zones, whether this zone is signed, and the DS record(s) to publish at the domain\'s registrar.',
            'x-permission' => 'dns.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('The DNSSEC state.', O::ref('DnsSecStatus'), ['supported' => true, 'reason' => '', 'backend' => 'powerdns', 'backend_label' => 'PowerDNS', 'enabled' => true, 'ds' => [['key_tag' => 12345, 'algorithm' => 13, 'digest_type' => 2, 'digest' => '…']], 'ds_error' => '']), '403' => O::err('Forbidden'), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
        ]],
        '/dns/zones/{id}/dnssec/enable' => ['post' => [
            'operationId' => 'enableDnsSec',
            'tags' => ['DNS zones'],
            'summary' => 'Turn DNSSEC on',
            'description' => 'Administrators only. Signs the zone. **Publish the returned DS record at the domain\'s registrar**, or resolvers that validate DNSSEC will fail the domain.',
            'x-permission' => 'dns.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('The DNSSEC state, with a `warning` when there is one.', O::ref('DnsSecStatus'), ['supported' => true, 'reason' => '', 'backend' => 'powerdns', 'backend_label' => 'PowerDNS', 'enabled' => true, 'ds' => [], 'ds_error' => '', 'warning' => '']), '403' => O::err('Forbidden'), '404' => O::err('NotFound'), '409' => O::err('Conflict'), '502' => O::err('AgentError')],
        ]],
        '/dns/zones/{id}/dnssec/disable' => ['post' => [
            'operationId' => 'disableDnsSec',
            'tags' => ['DNS zones'],
            'summary' => 'Turn DNSSEC off',
            'description' => 'Administrators only. Remove the DS record at the registrar **first**, or validating resolvers will fail the domain.',
            'x-permission' => 'dns.manage',
            'parameters' => [$id],
            'responses' => ['200' => O::res('The DNSSEC state.', O::ref('DnsSecStatus'), ['supported' => true, 'reason' => '', 'backend' => 'powerdns', 'backend_label' => 'PowerDNS', 'enabled' => false, 'ds' => [], 'ds_error' => '', 'warning' => '']), '403' => O::err('Forbidden'), '404' => O::err('NotFound'), '502' => O::err('AgentError')],
        ]],
    ],
];
