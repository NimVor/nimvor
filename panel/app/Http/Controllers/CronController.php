<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\CronService;
use WhaCP\Services\JobTracker;
use WhaCP\Services\ServerContext;
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

final class CronController extends SiteController
{
    public function __construct(
        View $view,
        SessionInterface $session,
        WebsiteRepository $sites,
        ServerContext $server,
        private readonly CronService $cron,
        private readonly JobTracker $jobs,
    ) {
        parent::__construct($view, $session, $sites, $server);
    }

    public function show(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $editId = (int) $request->query('edit', 0);
        return $this->render('sites/cron', [
            'site' => $site,
            'jobs' => $this->cron->jobs($site),
            'edit' => $editId > 0 ? $this->cron->find($site, $editId) : null,
        ]);
    }

    /** A job's runs from its log: start, duration, exit code, output. */
    public function log(Request $request, string $id, string $job): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $j = $this->cron->find($site, (int) $job);
        if ($j === null) {
            return $this->redirectWithError('/sites/' . (int) $id . '/cron', 'Cron job not found.');
        }
        try {
            $log = $this->cron->runs($site, $j);
            $error = null;
        } catch (\WhaCP\Agent\AgentException $e) {
            $log = ['runs' => [], 'truncated' => false, 'size' => 0];
            $error = $e;
        }
        return $this->render('sites/cron_log', ['site' => $site, 'job' => $j, 'log' => $log, 'agentError' => $error, 'tab' => 'cron']);
    }

    public function store(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'cron', function (array $site) use ($request): string {
            $jobId = (int) $request->input('job_id', 0);
            $this->cron->save($site, $jobId > 0 ? $jobId : null, $request->all(), (string) ($this->user($request)['email'] ?? ''));
            return $jobId > 0 ? 'Cron job updated.' : 'Cron job added. It runs as ' . $site['system_user'] . '.';
        });
    }

    public function toggle(Request $request, string $id, string $job): Response
    {
        return $this->act($request, $id, 'cron', function (array $site) use ($request, $job): string {
            $j = $this->cron->find($site, (int) $job) ?? throw new UserFacingException('Cron job not found.');
            $this->cron->setEnabled($site, (int) $j['id'], !$j['enabled']);
            return $j['enabled'] ? 'Cron job paused.' : 'Cron job enabled.';
        });
    }

    public function delete(Request $request, string $id, string $job): Response
    {
        return $this->act($request, $id, 'cron', function (array $site) use ($job): string {
            $j = $this->cron->find($site, (int) $job) ?? throw new UserFacingException('Cron job not found.');
            $this->cron->delete($site, (int) $j['id']);
            return 'Cron job deleted.';
        });
    }

    public function run(Request $request, string $id, string $job): Response
    {
        return $this->act($request, $id, 'cron', function (array $site) use ($request, $job): array {
            $j = $this->cron->find($site, (int) $job) ?? throw new UserFacingException('Cron job not found.');
            $jobId = $this->cron->run($site, $j);
            $this->jobs->track($jobId, 'Cron: ' . mb_substr((string) $j['command'], 0, 60), 'website', (string) $site['id'], $this->userId($request));
            return ['Command started.', $jobId];
        });
    }
}
