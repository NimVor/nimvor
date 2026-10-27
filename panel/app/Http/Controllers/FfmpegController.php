<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Database\Connection;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\FfmpegService;
use WhaCP\Services\JobTracker;
use WhaCP\Services\ServerContext;
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

/** FFmpeg: installation, version, and access per hosting plan. */
final class FfmpegController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly FfmpegService $ffmpeg,
        private readonly AgentClient $agent,
        private readonly JobTracker $jobs,
        private readonly ServerContext $server,
        private readonly AuditLogger $audit,
        private readonly Connection $db,
    ) {
        parent::__construct($view, $session);
    }

    private function denied(Request $request): ?Response
    {
        return \WhaCP\Hosting\Scope::isAll($this->user($request)) ? null : $this->render('errors/error', ['title' => 'Access denied', 'message' => 'FFmpeg is managed by server administrators.', 'status' => 403], 403);
    }

    public function index(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $status = null;
        $error = null;
        try {
            $status = $this->ffmpeg->status();
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('server/ffmpeg', [
            'status' => $status, 'agentError' => $error, 'allowed' => $this->ffmpeg->allowedUsers(),
            'plans' => $this->db->fetchAll('SELECT p.id, p.name, p.ffmpeg, COALESCE(o.username, \'\') AS owner, (SELECT COUNT(*) FROM users u WHERE u.plan_id = p.id) AS users FROM plans p LEFT JOIN users o ON o.id = p.owner_id ORDER BY p.owner_id IS NOT NULL, p.name'),
        ]);
    }

    private function job(Request $request, string $action, array $params, string $label): Response
    {
        try {
            $job = (string) ((array) $this->agent->call($action, $params))['job_id'];
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/ffmpeg', $e);
        }
        $this->jobs->track($job, $label, 'server', (string) $this->server->id(), $this->userId($request));
        $this->audit->record($action, 'server', (string) $this->server->id(), AuditLogger::SUCCESS, $params);
        $this->flashJob($job);
        return $this->redirectWithSuccess('/ffmpeg', $label . ' started. Reload the page when it has finished.');
    }

    public function install(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        return $this->job($request, 'packages.install', ['name' => 'ffmpeg'], 'Install or update FFmpeg');
    }

    public function uninstall(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        return $this->job($request, 'ffmpeg.uninstall', [], 'Uninstall FFmpeg');
    }

    public function apply(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        try {
            $log = $this->ffmpeg->reconcile(true);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/ffmpeg', $e);
        }
        return $this->redirectWithSuccess('/ffmpeg', $log !== [] ? implode(' ', $log) : 'FFmpeg access matches the plans.');
    }
}
