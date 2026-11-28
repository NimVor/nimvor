<?php

declare(strict_types=1);

use NimVor\Api\OpenApi as O;

$id = ['name' => 'id', 'in' => 'path', 'required' => true, 'description' => 'The website\'s id.', 'schema' => ['type' => 'integer', 'minimum' => 1]];
$range = O::query('range', 'The period: `1h`, `24h` (default), `7d` or `30d`.', ['type' => 'string', 'enum' => ['1h', '24h', '7d', '30d']]);

return [
    'tags' => [
        ['name' => 'Resources', 'description' => "What websites use, recorded with every monitoring pass: the CPU, memory and disk bytes of a website's own PHP-FPM workers (sampled), its requests, traffic and errors from its own access log, and the kernel's counters of the account's limits (shared by the account's websites). A value that was not measured is `null`, never zero. Read-only; needs the websites permission and only shows websites the token may see."],
    ],
    'paths' => [
        '/websites/{id}/resources' => ['get' => [
            'operationId' => 'getWebsiteResources',
            'tags' => ['Resources'],
            'summary' => 'A website\'s resource use',
            'description' => 'The latest sample, the history of the period (`cpu` in percent of one core, `mem` and `mem_max` in bytes, `read` and `write` in bytes per second, `rpm` requests per minute, `bw_out` bytes per second, `e4` and `e5` per minute), the account\'s limit counters and the faults and warnings of the period. A fault is a limit that was actually hit; a warning is a threshold crossed.',
            'x-permission' => 'websites.view',
            'parameters' => [$id, $range],
            'responses' => ['200' => O::res('The readings.', ['type' => 'object'], ['domain' => 'example.com', 'range' => '24h', 'current' => null, 'account' => null, 'websites_in_account' => 0, 'points' => [], 'account_points' => [], 'events' => []]), '404' => O::err('NotFound')],
        ]],
        '/resources/ranking' => ['get' => [
            'operationId' => 'getResourceRanking',
            'tags' => ['Resources'],
            'summary' => 'Websites ranked by what they use',
            'description' => 'The websites the token may see, with what each used in the period. Sort by `cpu`, `memory`, `io`, `requests`, `traffic`, `errors` or `faults`; `q` filters by part of the domain and `faults=1` keeps websites whose account had a fault.',
            'x-permission' => 'websites.view',
            'parameters' => [$range, O::query('sort', 'The sort key (default `cpu`).', ['type' => 'string', 'enum' => ['cpu', 'memory', 'io', 'requests', 'traffic', 'errors', 'faults']]), O::query('q', 'Part of a domain.', ['type' => 'string']), O::query('faults', '`1`: only websites with faults.', ['type' => 'string', 'enum' => ['1']]), O::query('limit', 'At most this many (1–500, default 100).', ['type' => 'integer', 'minimum' => 1, 'maximum' => 500])],
            'responses' => ['200' => O::res('The ranking.', ['type' => 'object'], ['range' => '24h', 'sort' => 'cpu', 'websites' => [['id' => 1, 'domain' => 'example.com', 'cpu' => 12.5, 'mem_max' => 268435456, 'requests' => 1000, 'faults' => 0]]])],
        ]],
    ],
];
