<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\AuditLogger;
use NimVor\Services\JobTracker;
use NimVor\Services\ServerContext;
use NimVor\Services\ServerDiskService;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

/** Server → Disk usage: filesystems, where the space goes, each website's share and the largest files. */
final class ServerDiskController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly ServerDiskService $disk,
        private readonly AuditLogger $audit,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly JobTracker $jobs,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $error = null;
        $filesystems = [];
        try {
            $this->disk->refresh();
            $filesystems = $this->disk->filesystems();
        } catch (AgentException $e) {
            $error = $e;
        }
        $tab = in_array($request->query('tab'), ['overview', 'websites', 'files'], true) ? (string) $request->query('tab') : 'overview';
        $siteIds = [];
        foreach ($this->sites->all($this->server->id(), $this->user($request)) as $w) {
            $siteIds[(string) $w['domain']] = (int) $w['id'];
        }
        return $this->render('server/disk', [
            'siteIds' => $siteIds, 'agentError' => $error, 'filesystems' => $filesystems, 'tab' => $tab,
            'scan' => $this->disk->latest(), 'running' => $this->disk->running(),
        ]);
    }

    public function scan(Request $request): Response
    {
        try {
            $job = $this->disk->scan((int) $request->input('min_mb', 50));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/server/disk', $e);
        }
        $this->jobs->track($job, 'Measure the server disk', 'server', (string) $this->server->id(), $this->userId($request));
        $this->flashJob($job);
        return $this->redirectWithSuccess('/server/disk', 'Measuring the disk. This can take a few minutes on a big server; the page shows the result when it is done.');
    }

    public function remove(Request $request): Response
    {
        $paths = array_map('strval', (array) $request->input('paths', []));
        try {
            $r = $this->disk->remove($paths);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/server/disk?tab=files', $e);
        }
        $done = array_values(array_filter($r['results'], static fn (array $x): bool => ($x['action'] ?? '') !== ''));
        $failed = count($r['results']) - count($done);
        $this->audit->record('server.disk.remove', 'server', 'disk', $failed === 0 ? AuditLogger::SUCCESS : AuditLogger::FAILURE, [
            'files' => count($done), 'failed' => $failed, 'freed_bytes' => $r['freed_bytes'], 'paths' => array_slice(array_column($done, 'path'), 0, 50),
        ]);
        $msg = count($done) . ' file(s) removed, ' . format_bytes($r['freed_bytes']) . ' freed.' . ($failed > 0 ? ' ' . $failed . ' could not be removed.' : '');
        return $failed === 0 || $done !== [] ? $this->redirectWithSuccess('/server/disk?tab=files', $msg) : $this->redirectWithError('/server/disk?tab=files', $msg);
    }
}
