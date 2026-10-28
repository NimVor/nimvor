<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\MonitoringService;
use NimVor\View\View;

final class MonitoringController extends Controller
{
    public const RANGES = ['24h' => 86400, '7d' => 604800, '30d' => 2592000];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly MonitoringService $monitoring,
        private readonly \NimVor\Agent\AgentClient $agent,
        private readonly ?\NimVor\Services\AuditLogger $audit = null,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $range = (string) $request->query('range', '24h');
        if (!isset(self::RANGES[$range])) {
            $range = '24h';
        }
        return $this->render('monitoring/index', [
            'range' => $range,
            'series' => $this->monitoring->series(self::RANGES[$range]),
            'open' => $this->monitoring->openAlerts(),
            'recent' => $this->monitoring->recentAlerts(),
        ]);
    }

    /** Live statistics (server administrators: all accounts' processes). */
    public function live(Request $request): Response
    {
        if (!\NimVor\Hosting\Scope::isAll($this->user($request))) {
            return $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Live statistics are for server administrators.', 'status' => 403], 403);
        }
        return $this->render('monitoring/live', []);
    }

    public function liveData(Request $request): Response
    {
        if (!\NimVor\Hosting\Scope::isAll($this->user($request))) {
            return Response::json(['error' => 'Access denied.'], 403);
        }
        try {
            return Response::json((array) json_decode((string) json_encode($this->agent->call('system.live', [], 20)), true));
        } catch (\NimVor\Agent\AgentException $e) {
            return Response::json(['error' => $e->getMessage()], 502);
        }
    }

    private function admin(Request $request): bool
    {
        return \NimVor\Hosting\Scope::isAll($this->user($request));
    }

    /** Process manager: every process, suspicious ones flagged. */
    public function processes(Request $request): Response
    {
        if (!$this->admin($request)) {
            return $this->render('errors/error', ['title' => 'Access denied', 'message' => 'The process manager is for server administrators.', 'status' => 403], 403);
        }
        $snap = null;
        $error = null;
        try {
            $snap = (array) json_decode((string) json_encode($this->agent->call('processes.list', [], 60)), true);
        } catch (\NimVor\Agent\AgentException $e) {
            $error = $e;
        }
        $show = (string) $request->query('show', '');
        return $this->render('monitoring/processes', ['snap' => $snap, 'agentError' => $error, 'show' => $show === 'suspicious' ? 'suspicious' : '']);
    }

    /** One process in detail, with its system calls for five seconds. */
    public function trace(Request $request): Response
    {
        if (!$this->admin($request)) {
            return $this->render('errors/error', ['title' => 'Access denied', 'message' => 'The process manager is for server administrators.', 'status' => 403], 403);
        }
        $pid = (int) $request->query('pid', '0');
        $start = (int) $request->query('start', '0');
        try {
            $trace = (array) json_decode((string) json_encode($this->agent->call('processes.trace', ['pid' => $pid, 'start' => $start], 60)), true);
        } catch (\NimVor\Agent\AgentException $e) {
            return $this->redirectWithFailure('/monitoring/processes', $e);
        }
        $this->audit?->record('processes.trace', 'process', (string) $pid);
        return $this->render('monitoring/trace', ['trace' => $trace, 'start' => $start]);
    }

    public function signal(Request $request): Response
    {
        $back = '/monitoring/processes' . ($request->string('show') === 'suspicious' ? '?show=suspicious' : '');
        if (!$this->admin($request)) {
            return $this->redirectWithError($back, 'The process manager is for server administrators.');
        }
        $pid = (int) $request->input('pid', '0');
        $sig = $request->string('signal');
        try {
            $this->agent->call('processes.signal', ['pid' => $pid, 'start' => (int) $request->input('start', '0'), 'signal' => $sig]);
        } catch (\NimVor\Support\UserFacingException $e) {
            $this->audit?->record('processes.signal', 'process', (string) $pid, \NimVor\Services\AuditLogger::FAILURE, ['signal' => $sig, 'command' => mb_substr($request->string('command'), 0, 200), 'error' => $e->getMessage()]);
            return $this->redirectWithFailure($back, $e);
        }
        $this->audit?->record('processes.signal', 'process', (string) $pid, \NimVor\Services\AuditLogger::SUCCESS, ['signal' => $sig, 'command' => mb_substr($request->string('command'), 0, 200)]);
        $verb = ['TERM' => 'asked to stop', 'KILL' => 'killed', 'HUP' => 'sent HUP', 'STOP' => 'paused', 'CONT' => 'resumed'][$sig] ?? 'signalled';
        return $this->redirectWithSuccess($back, 'Process ' . $pid . ' ' . $verb . '.');
    }

    public function killUser(Request $request): Response
    {
        if (!$this->admin($request)) {
            return $this->redirectWithError('/monitoring/processes', 'The process manager is for server administrators.');
        }
        $user = $request->string('user');
        $sig = $request->string('signal') === 'KILL' ? 'KILL' : 'TERM';
        try {
            $res = (array) $this->agent->call('processes.kill_user', ['user' => $user, 'signal' => $sig]);
        } catch (\NimVor\Support\UserFacingException $e) {
            $this->audit?->record('processes.kill_user', 'account', $user, \NimVor\Services\AuditLogger::FAILURE, ['signal' => $sig, 'error' => $e->getMessage()]);
            return $this->redirectWithFailure('/monitoring/processes', $e);
        }
        $this->audit?->record('processes.kill_user', 'account', $user, \NimVor\Services\AuditLogger::SUCCESS, ['signal' => $sig, 'signalled' => $res['signalled'] ?? 0]);
        return $this->redirectWithSuccess('/monitoring/processes', (int) ($res['signalled'] ?? 0) . ' process(es) of ' . $user . ($sig === 'KILL' ? ' killed.' : ' asked to stop.'));
    }
}
