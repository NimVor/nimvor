<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers\Api;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\ServerContext;
use NimVor\Services\SiteResourceService;

/**
 * REST API v1: what websites use. Read-only, from the same recorded history as the website's Resources page:
 * a website's current readings, history and faults, and the ranking of the websites the token may see. A value
 * that was not measured is null.
 */
final class ResourcesApiController
{
    use ApiHelpers;

    public function __construct(
        private readonly SiteResourceService $resources,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
    ) {
    }

    public function site(Request $r, string $id): Response
    {
        $site = $this->sites->find($this->server->id(), (int) $id, $this->user($r));
        if ($site === null) {
            return self::notFound();
        }
        $range = SiteResourceService::range((string) $r->query('range', '24h'));
        $cur = $this->resources->current((int) $site['id']);
        return self::ok([
            'domain' => $site['domain'], 'range' => $range,
            'current' => $cur['site'], 'account' => $cur['account'], 'websites_in_account' => $cur['sites_in_account'],
            'points' => $this->resources->series((int) $site['id'], $range),
            'account_points' => ($acct = (string) ($cur['site']['account'] ?? '')) !== '' ? $this->resources->accountSeries($acct, $range) : [],
            'events' => $this->resources->events((int) $site['id'], $range),
        ]);
    }

    public function ranking(Request $r): Response
    {
        $sort = (string) $r->query('sort', 'cpu');
        return self::ok([
            'range' => SiteResourceService::range((string) $r->query('range', '24h')), 'sort' => $sort,
            'websites' => $this->resources->ranking($this->user($r), (string) $r->query('range', '24h'), $sort, ['q' => (string) $r->query('q', ''), 'faults' => $r->query('faults') === '1'], (int) $r->query('limit', 100)),
        ]);
    }
}
