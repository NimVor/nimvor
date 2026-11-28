<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Database\Connection;
use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\AuditLogger;
use NimVor\Services\ServerContext;
use NimVor\Services\SiteResourceService;
use NimVor\View\View;

/** A website's Resource usage page (what it used, its account's limits, faults) and the server-wide ranking. */
final class SiteResourcesController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly SiteResourceService $resources,
        private readonly WebsiteRepository $websites,
        private readonly ServerContext $server,
        private readonly Connection $db,
        private readonly AuditLogger $audit,
    ) {
        parent::__construct($view, $session);
    }

    /** @return array<string, mixed>|null a website the signed-in user may see */
    private function site(Request $request, string $id): ?array
    {
        return $this->websites->find($this->server->id(), (int) $id, $this->user($request));
    }

    private function notFound(): Response
    {
        return $this->render('errors/error', ['title' => 'Website not found', 'message' => 'This website does not exist or you do not have access to it.', 'status' => 404], 404);
    }

    public function show(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $range = SiteResourceService::range((string) $request->query('range', '24h'));
        $wid = (int) $site['id'];
        return $this->render('sites/resources', [
            'site' => $site, 'tab' => 'resources', 'range' => $range,
            'current' => $this->resources->current($wid),
            'events' => $this->resources->events($wid, $range),
            'accountPoints' => ($account = (string) ($this->resources->current($wid)['site']['account'] ?? '')) !== '' ? $this->resources->accountSeries($account, $range) : [],
            'first' => $this->db->scalar('SELECT MIN(taken_at) FROM site_metric_samples WHERE website_id = ?', [$wid]),
            'fpm' => $this->resources->fpmStatus(),
            'admin' => Scope::isAll($this->user($request)),
        ]);
    }

    /** The chart data of the website's page, in the shape the dashboard charts read. */
    public function series(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return Response::json(['success' => false, 'error' => ['message' => 'Not found.']], 404);
        }
        $range = SiteResourceService::range((string) $request->query('range', '24h'));
        $now = time();
        $wid = (int) $site['id'];
        $first = $this->db->scalar('SELECT MIN(taken_at) FROM site_metric_samples WHERE website_id = ?', [$wid]);
        return Response::json([
            'success' => true, 'range' => $range, 'from' => $now - SiteResourceService::RANGES[$range][0], 'to' => $now,
            'since' => $first === null || $first === false ? null : (int) $first, 'points' => $this->resources->series($wid, $range, $now),
        ]);
    }

    public function ranking(Request $request): Response
    {
        $range = SiteResourceService::range((string) $request->query('range', '24h'));
        $sort = (string) $request->query('sort', 'cpu');
        $q = trim((string) $request->query('q', ''));
        $faults = $request->query('faults') === '1';
        return $this->render('resources/index', [
            'rows' => $this->resources->ranking($this->user($request), $range, $sort, ['q' => $q, 'faults' => $faults]),
            'range' => $range, 'sort' => $sort, 'q' => $q, 'faults' => $faults,
            'admin' => Scope::isAll($this->user($request)), 'fpm' => Scope::isAll($this->user($request)) ? $this->resources->fpmStatus() : null,
        ]);
    }

    /** An administrator switches PHP-FPM's status page on or off for the websites' pools (opt-in; see the page for what it changes). */
    public function fpmStatus(Request $request): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Only server administrators can change this.', 'status' => 403], 403);
        }
        $enable = $request->input('enabled') === '1';
        try {
            $r = $this->resources->setFpmStatus($enable);
        } catch (AgentException $e) {
            $this->audit->record('resources.fpm_status', 'server', 'php-fpm', AuditLogger::FAILURE, ['enabled' => $enable, 'error' => $e->getMessage()]);
            return $this->redirectWithFailure('/resources', $e);
        }
        $this->audit->record('resources.fpm_status', 'server', 'php-fpm', AuditLogger::SUCCESS, ['enabled' => $enable, 'pools' => $r['with_status'], 'skipped' => count($r['skipped'])]);
        $msg = $enable ? 'PHP-FPM status is on for ' . $r['with_status'] . ' of ' . $r['php_sites'] . ' PHP websites. The numbers appear from the next monitoring pass.' : 'PHP-FPM status is off again.';
        if ($r['skipped'] !== []) {
            $msg .= ' Left alone (edited by hand or without a pool file): ' . implode(', ', array_slice($r['skipped'], 0, 5)) . '.';
        }
        return $this->redirectWithSuccess('/resources', $msg);
    }
}
