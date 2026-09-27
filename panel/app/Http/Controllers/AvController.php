<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\AvService;
use WhaCP\Services\JobTracker;
use WhaCP\Services\SecurityService;
use WhaCP\Services\ServerContext;
use WhaCP\Services\SettingsService;
use WhaCP\Support\UserFacingException;
use WhaCP\Support\ValidationException;
use WhaCP\View\View;

/** Malware scans (ClamAV): the server's Security tab and each website's page. */
final class AvController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AvService $av,
        private readonly SettingsService $settings,
        private readonly SecurityService $security,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly JobTracker $jobs,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $status = null;
        $error = null;
        try {
            $status = $this->av->status();
            $this->av->refresh();
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('security/malware', [
            'tab' => 'malware', 'pending' => $this->security->pending(), 'agentError' => $error,
            'status' => $status, 'scans' => $this->av->recent(),
            'days' => $this->settings->int('av_scan_days'), 'quarantine' => $this->settings->bool('av_quarantine'),
        ]);
    }

    public function saveSettings(Request $request): Response
    {
        try {
            $this->settings->update(['av_scan_days' => $request->input('av_scan_days'), 'av_quarantine' => $request->input('av_quarantine') === '1'], $this->userId($request));
        } catch (ValidationException $e) {
            return $this->redirectWithError('/security/malware', implode(' ', $e->errors()));
        }
        return $this->redirectWithSuccess('/security/malware', 'Malware scan settings saved.');
    }

    public function updateSignatures(Request $request): Response
    {
        try {
            $this->av->updateSignatures($this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/security/malware', $e);
        }
        return $this->redirectWithSuccess('/security/malware', 'Downloading the latest virus signatures. This takes a few minutes.');
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
        $error = null;
        try {
            $status = $this->av->status();
            if ($this->av->running((int) $site['id']) !== null) {
                $this->av->refresh();
            }
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('sites/malware', [
            'site' => $site, 'tab' => 'malware', 'agentError' => $error, 'status' => $status,
            'scans' => $this->av->history((int) $site['id']), 'running' => $this->av->running((int) $site['id']),
            'quarantine' => $this->settings->bool('av_quarantine'), 'days' => $this->settings->int('av_scan_days'),
        ]);
    }

    public function scan(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->redirectWithError('/websites', 'Website not found.');
        }
        $to = '/sites/' . (int) $site['id'] . '/malware';
        try {
            $job = $this->av->scan($site, $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        $this->jobs->track($job, 'Malware scan of ' . $site['domain'], 'website', (string) $site['id'], $this->userId($request));
        $this->flashJob($job);
        return $this->redirectWithSuccess($to, 'Scanning ' . $site['domain'] . '. You are notified of the result.');
    }
}
