<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentClient;
use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\AuditLogger;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

/**
 * Database server (MariaDB / MySQL) manager for server administrators:
 * status, connections, NimVor's managed settings, flush and a checked
 * restart. Everything runs in the agent.
 */
final class DbServerController extends Controller
{
    private const PAGE = '/databases/server';

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
    ) {
        parent::__construct($view, $session);
    }

    private function denied(Request $request): ?Response
    {
        return Scope::isAll($this->user($request)) ? null : $this->render('errors/error', ['title' => 'Access denied', 'message' => 'The database server is managed by server administrators.', 'status' => 403], 403);
    }

    public function index(Request $request): Response
    {
        return $this->page($request, 'overview');
    }

    public function settingsPage(Request $request): Response
    {
        return $this->page($request, 'settings');
    }

    public function connections(Request $request): Response
    {
        return $this->page($request, 'connections');
    }

    /** The database server's logs (the log viewer with its sources only). */
    public function logs(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $sources = [];
        $error = null;
        try {
            $sources = array_values(array_filter((array) $this->agent->call('logs.sources'), static function ($s): bool {
                $id = (string) (is_array($s) ? ($s['id'] ?? '') : ($s->id ?? ''));
                return (bool) preg_match('/mysql|mariadb/i', $id);
            }));
        } catch (AgentException $e) {
            $error = $e;
        }
        $sources = json_decode((string) json_encode($sources), true);
        return $this->render('databases/logs', ['sources' => $sources, 'agentError' => $error, 'site' => null, 'initialSource' => (string) ($sources[0]['id'] ?? '')]);
    }

    private function page(Request $request, string $section): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $status = null;
        $conns = null;
        $error = null;
        try {
            $status = json_decode((string) json_encode($this->agent->call('db.server_status', [], 60)), true);
            if (!empty($status['running']) && $section === 'connections') {
                $conns = json_decode((string) json_encode($this->agent->call('db.processlist', [], 30)), true);
            }
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('databases/server', ['status' => $status, 'connections' => $conns['connections'] ?? [], 'agentError' => $error, 'section' => $section]);
    }

    public function saveSettings(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $in = array_map(static fn (mixed $v): string => is_scalar($v) ? trim((string) $v) : '', (array) $request->input('settings', []));
        $managed = array_map(static fn (mixed $v): string => is_scalar($v) ? (string) $v : '', (array) $request->input('managed', []));
        // Send only what changed: a value that differs from NimVor's file, or a field emptied that was set.
        $send = [];
        foreach ($in as $k => $v) {
            if ($v !== ($managed[$k] ?? '')) {
                $send[$k] = $v;
            }
        }
        $restart = $request->input('restart') === '1';
        if ($send === [] && !$restart) {
            return $this->redirectWithSuccess(self::PAGE . '/settings', 'Nothing changed.');
        }
        try {
            $res = (array) $this->agent->call('db.server_settings', ['settings' => (object) $send, 'restart' => $restart], 200);
        } catch (UserFacingException $e) {
            $this->audit->record('database.server_settings', 'server', 'database', AuditLogger::FAILURE, ['settings' => $send, 'restart' => $restart, 'error' => $e->getMessage()]);
            return $this->redirectWithFailure(self::PAGE . '/settings', $e);
        }
        $this->audit->record('database.server_settings', 'server', 'database', AuditLogger::SUCCESS, ['settings' => $send, 'restart' => $restart]);
        $pending = (array) ($res['restart_needed'] ?? []);
        $msg = !empty($res['restarted']) ? 'Settings saved; the database server was restarted with them.' : 'Settings saved and applied.';
        if ($pending !== []) {
            $msg = 'Settings saved. ' . implode(', ', array_map('strval', $pending)) . ' take effect after the database server restarts.';
        }
        return $this->redirectWithSuccess(self::PAGE . '/settings', $msg);
    }

    public function restart(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        try {
            $res = (array) $this->agent->call('db.server_restart', [], 200);
        } catch (UserFacingException $e) {
            $this->audit->record('database.server_restart', 'server', 'database', AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            return $this->redirectWithFailure(self::PAGE, $e);
        }
        $this->audit->record('database.server_restart', 'server', 'database', AuditLogger::SUCCESS, []);
        return $this->redirectWithSuccess(self::PAGE, 'The database server restarted' . (isset($res['seconds']) ? ' in ' . (int) $res['seconds'] . ' s' : '') . ' and answers again.');
    }

    public function flush(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $what = $request->string('what');
        try {
            $this->agent->call('db.server_flush', ['what' => $what], 60);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::PAGE, $e);
        }
        $this->audit->record('database.server_flush', 'server', 'database', AuditLogger::SUCCESS, ['what' => $what]);
        return $this->redirectWithSuccess(self::PAGE, ['privileges' => 'Privileges reloaded.', 'logs' => 'Log files reopened.', 'hosts' => 'Blocked hosts cleared.'][$what] ?? 'Done.');
    }

    public function kill(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $id = (int) $request->input('id', 0);
        $queryOnly = $request->input('query_only') === '1';
        try {
            $this->agent->call('db.kill', ['id' => $id, 'query_only' => $queryOnly], 30);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::PAGE . '/connections', $e);
        }
        $this->audit->record('database.kill', 'server', 'database', AuditLogger::SUCCESS, ['id' => $id, 'query_only' => $queryOnly]);
        return $this->redirectWithSuccess(self::PAGE . '/connections', $queryOnly ? 'The statement of connection ' . $id . ' was stopped.' : 'Connection ' . $id . ' was ended.');
    }
}
