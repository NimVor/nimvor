<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Hosting\Scope;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\JobTracker;
use WhaCP\Services\ServerContext;
use WhaCP\Services\UpdateCheckService;
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

/**
 * The top bar's server tools for administrators: the load (refreshed while
 * a page is open), restarting every service at once, and updating NimVor.
 */
final class TopbarController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly JobTracker $jobs,
        private readonly ServerContext $server,
        private readonly UpdateCheckService $updates,
    ) {
        parent::__construct($view, $session);
    }

    private function admin(Request $request): bool
    {
        return Scope::isAll($this->user($request));
    }

    /** Load averages (and whether an update is available), polled by the top bar. */
    public function status(Request $request): Response
    {
        if (!$this->admin($request)) {
            return Response::json(['success' => false], 403);
        }
        try {
            $st = (array) $this->agent->call('agent.status', [], 10);
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], 503);
        }
        return Response::json(['success' => true, 'load' => $st['load'] ?? null, 'update' => $this->updates->available()]);
    }

    /** Restart every running service NimVor manages, then the agent and the panel. */
    public function restartAll(Request $request): Response
    {
        $back = \WhaCP\Http\Middleware\Authenticate::safePath($request->header('Referer'));
        if (!$this->admin($request)) {
            return $this->redirectWithError($back, 'Only server administrators can restart every service.');
        }
        try {
            $res = (array) $this->agent->call('services.restart_all');
        } catch (UserFacingException $e) {
            $this->audit->record('services.restart_all', 'server', (string) $this->server->id(), AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            return $this->redirectWithFailure($back, $e);
        }
        $this->jobs->track((string) $res['job_id'], 'Restart all services', 'server', (string) $this->server->id(), $this->userId($request));
        $this->audit->record('services.restart_all', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['services' => $res['services'] ?? []]);
        $this->flashJob((string) $res['job_id']);
        return $this->redirectWithSuccess($back, 'Restarting ' . count((array) ($res['services'] ?? [])) . ' services; the panel restarts last, so this page may not answer for a few seconds.');
    }

    /** The NimVor update page: installed and newest version, the last update's output. */
    public function update(Request $request): Response
    {
        if (!$this->admin($request)) {
            return $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Updating NimVor is for server administrators.', 'status' => 403], 403);
        }
        $status = null;
        $error = null;
        try {
            $status = (array) json_decode((string) json_encode($this->agent->call('whacp.update_status')), true);
        } catch (AgentException $e) {
            $error = $e;
        }
        if ($request->wantsJson()) {
            return $error === null ? Response::json(['success' => true, 'running' => !empty($status['running']), 'log' => (string) ($status['log'] ?? ''), 'result' => (string) ($status['last_result'] ?? '')])
                : Response::json(['success' => false, 'error' => $error->toArray()], 503);
        }
        return $this->render('server/update', ['status' => $status, 'check' => $this->updates->cached(), 'agentError' => $error]);
    }

    public function updateCheck(Request $request): Response
    {
        if (!$this->admin($request)) {
            return $this->redirectWithError('/whacp/update', 'Updating NimVor is for server administrators.');
        }
        $c = $this->updates->check();
        return !empty($c['available']) ? $this->redirectWithSuccess('/whacp/update', 'A newer version of NimVor is available.')
            : ($c['state'] === 'current' ? $this->redirectWithSuccess('/whacp/update', 'NimVor is up to date.') : $this->redirectWithError('/whacp/update', 'The newest version could not be looked up. ' . (string) $c['reason']));
    }

    public function updateStart(Request $request): Response
    {
        if (!$this->admin($request)) {
            return $this->redirectWithError('/whacp/update', 'Updating NimVor is for server administrators.');
        }
        try {
            $this->agent->call('whacp.update', [], 30);
        } catch (UserFacingException $e) {
            $this->audit->record('whacp.update', 'server', (string) $this->server->id(), AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            return $this->redirectWithFailure('/whacp/update', $e);
        }
        $this->audit->record('whacp.update', 'server', (string) $this->server->id());
        return $this->redirectWithSuccess('/whacp/update', 'The update is running. The panel restarts during it; this page follows its progress.');
    }
}
