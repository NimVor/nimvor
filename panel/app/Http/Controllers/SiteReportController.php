<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\ServerContext;
use NimVor\Services\SiteReportService;
use NimVor\View\View;

/** A website's traffic report. */
final class SiteReportController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly SiteReportService $reports,
        private readonly WebsiteRepository $websites,
        private readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    public function show(Request $request, string $id): Response
    {
        $site = $this->websites->find($this->server->id(), (int) $id, $this->user($request));
        if ($site === null) {
            return $this->render('errors/error', ['title' => 'Website not found', 'message' => 'This website does not exist or you do not have access to it.', 'status' => 404], 404);
        }
        $hours = SiteReportService::window($request->query('hours', 24));
        $report = null;
        $error = null;
        try {
            $report = $this->reports->build($site, $hours);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('sites/report', ['site' => $site, 'tab' => 'report', 'agentError' => $error, 'report' => $report, 'hours' => $hours, 'windows' => SiteReportService::WINDOWS]);
    }
}
