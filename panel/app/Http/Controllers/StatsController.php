<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\JobTracker;
use NimVor\Services\ServerContext;
use NimVor\Services\StatsService;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

/** Website statistics (AWStats). */
final class StatsController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly StatsService $stats,
        private readonly JobTracker $jobs,
    ) {
        parent::__construct($view, $session);
    }

    /** @return array<string, mixed>|null */
    private function site(Request $request, string $id): ?array
    {
        return $this->sites->find($this->server->id(), (int) $id, $this->user($request));
    }

    public function show(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->render('errors/error', ['title' => 'Website not found', 'message' => 'This website does not exist or you do not have access to it.', 'status' => 404], 404);
        }
        $status = null;
        $report = null;
        $error = null;
        $month = (string) $request->query('month', '');
        try {
            $status = $this->stats->status($site);
            if ($status['months'] !== []) {
                if (!in_array($month, $status['months'], true)) {
                    $month = $status['months'][0];
                }
                $report = $this->stats->report($site, $month);
            }
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('sites/stats', ['site' => $site, 'status' => $status, 'report' => $report, 'month' => $month, 'agentError' => $error, 'tab' => 'stats']);
    }

    public function enable(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->redirectWithError('/websites', 'Website not found.');
        }
        $on = $request->input('enabled') === '1';
        try {
            $this->stats->setEnabled($site, $on);
            if ($on) {
                $job = $this->stats->update($site);
                $this->jobs->track($job, 'Statistics of ' . $site['domain'], 'website', (string) $site['id'], $this->userId($request));
            }
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/sites/' . (int) $id . '/stats', $e);
        }
        return $this->redirectWithSuccess('/sites/' . (int) $id . '/stats', $on ? 'Statistics are on. They are updated every 6 hours; the first update is running now.' : 'Statistics are off. The collected data is kept.');
    }

    public function update(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->redirectWithError('/websites', 'Website not found.');
        }
        try {
            $job = $this->stats->update($site);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/sites/' . (int) $id . '/stats', $e);
        }
        $this->jobs->track($job, 'Statistics of ' . $site['domain'], 'website', (string) $site['id'], $this->userId($request));
        $this->flashJob($job);
        return $this->redirectWithSuccess('/sites/' . (int) $id . '/stats', 'Updating the statistics.');
    }
}
