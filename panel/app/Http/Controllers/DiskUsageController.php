<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\DiskUsageService;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

/** Disk usage by folder of a website. */
final class DiskUsageController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly DiskUsageService $usage,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
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
        try {
            $path = DiskUsageService::cleanPath((string) $request->query('path', ''));
        } catch (UserFacingException) {
            $path = '';
        }
        $error = null;
        try {
            $this->usage->refresh((int) $site['id']);
        } catch (AgentException $e) {
            $error = $e;
        }
        $sort = in_array($request->query('sort'), ['name', 'size', 'files'], true) ? (string) $request->query('sort') : 'size';
        return $this->render('sites/disk_usage', [
            'site' => $site, 'tab' => 'disk', 'path' => $path, 'sort' => $sort, 'agentError' => $error,
            'scan' => $this->usage->latest((int) $site['id'], $path), 'running' => $this->usage->running((int) $site['id'], $path),
        ]);
    }

    public function scan(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->redirectWithError('/websites', 'Website not found.');
        }
        $path = $request->string('path');
        $to = '/sites/' . (int) $site['id'] . '/disk-usage' . ($path !== '' ? '?path=' . rawurlencode($path) : '');
        try {
            $job = $this->usage->scan($site, $path);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        $this->flashJob($job);
        return $this->redirectWithSuccess($to, 'Measuring the folders. Large websites take a while; reload the page when the task has finished.');
    }
}
