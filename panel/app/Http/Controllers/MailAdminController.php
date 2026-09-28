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
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

/**
 * Email administration for server administrators: the mail queue,
 * delivery reports and statistics from the mail logs.
 */
final class MailAdminController extends Controller
{
    public const RANGES = ['1h' => 3600, '24h' => 86400, '7d' => 604800, '30d' => 2592000];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly \WhaCP\Services\MailService $mail,
    ) {
        parent::__construct($view, $session);
    }

    private function denied(Request $request): ?Response
    {
        return Scope::isAll($this->user($request)) ? null : $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Email administration is for server administrators.', 'status' => 403], 403);
    }

    public function queue(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $q = null;
        $error = null;
        try {
            $q = (array) $this->agent->call('mail.queue', [], 60);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('mail/admin/queue', ['tab' => 'queue', 'queue' => $q, 'agentError' => $error]);
    }

    public function message(Request $request, string $id): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        try {
            $res = (array) $this->agent->call('mail.queue_message', ['id' => $id]);
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => ['message' => $e->getMessage()]], 404);
        }
        return Response::json(['success' => true, 'headers' => (array) ($res['headers'] ?? [])]);
    }

    public function queueAction(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $action = $request->string('action');
        $ids = array_values(array_filter(array_map('strval', (array) $request->input('ids', [])), static fn (string $i): bool => preg_match('/^[0-9A-Za-z]{6,20}$/', $i) === 1));
        try {
            if ($action !== 'flush' && $ids === []) {
                throw new UserFacingException('Select messages first.');
            }
            $res = (array) $this->agent->call('mail.queue_action', ['action' => $action, 'ids' => $ids], 120);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/mail/admin/queue', $e);
        }
        $this->audit->record('mail.queue_' . $action, 'server', 'mail', AuditLogger::SUCCESS, ['count' => count($ids)]);
        $done = ['delete' => 'deleted', 'retry' => 'sent for delivery', 'hold' => 'put on hold', 'release' => 'released', 'requeue' => 'requeued'];
        return $this->redirectWithSuccess('/mail/admin/queue', $action === 'flush' ? 'Delivery of every deferred message is being retried.' : count($ids) . ' message(s) ' . ($done[$action] ?? $action) . '.');
    }

    public function reports(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $range = (string) $request->query('range', '24h');
        $seconds = self::RANGES[$range] ?? 86400;
        $filters = ['address' => trim((string) $request->query('address', '')), 'status' => (string) $request->query('status', '')];
        $tab = $request->query('view', '') === 'stats' ? 'stats' : 'reports';
        $rep = null;
        $error = null;
        try {
            $rep = (array) $this->agent->call('mail.report', [
                'since' => gmdate('c', time() - $seconds), 'until' => gmdate('c'),
                'address' => mb_substr($filters['address'], 0, 254), 'status' => $filters['status'], 'limit' => 500,
            ], 120);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('mail/admin/reports', ['tab' => $tab, 'report' => $rep === null ? null : json_decode((string) json_encode($rep['report']), true), 'source' => (string) ($rep['source'] ?? ''), 'range' => $range, 'filters' => $filters, 'agentError' => $error]);
    }

    public function server(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $status = null;
        $log = null;
        $error = null;
        try {
            $status = json_decode((string) json_encode($this->agent->call('mail.server_status', [], 60)), true);
            $log = json_decode((string) json_encode($this->agent->call('mail.log_tail', ['lines' => 100], 60)), true);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('mail/admin/server', ['tab' => 'server', 'status' => $status, 'log' => $log, 'agentError' => $error]);
    }

    public function saveSettings(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $pf = array_map('strval', (array) $request->input('postfix', []));
        $dc = array_map('strval', (array) $request->input('dovecot', []));
        // Only fields that changed are sent (unchanged Postfix values stay as Postfix has them).
        $current = (array) ($request->input('current', []));
        foreach ($pf as $k => $v) {
            if (isset($current[$k]) && (string) $current[$k] === $v) {
                unset($pf[$k]);
            }
        }
        try {
            $this->agent->call('mail.settings_set', ['postfix' => (object) $pf, 'dovecot' => (object) $dc], 120);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/mail/admin/server', $e);
        }
        $this->audit->record('mail.settings', 'server', 'mail', AuditLogger::SUCCESS, ['postfix' => $pf, 'dovecot' => $dc]);
        return $this->redirectWithSuccess('/mail/admin/server', 'Mail server settings applied.');
    }

    /** Mail troubleshooter and mailbox permission repair. */
    public function tools(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $address = mb_substr(trim((string) $request->query('address', '')), 0, 254);
        $trace = null;
        $error = null;
        if ($address !== '') {
            try {
                $trace = (array) json_decode((string) json_encode($this->agent->call('mail.trace', ['address' => $address], 60)), true);
            } catch (UserFacingException $e) {
                $error = $e;
            }
        }
        return $this->render('mail/admin/tools', ['tab' => 'tools', 'address' => $address, 'trace' => $trace, 'domains' => array_column($this->mail->domains($this->user($request)), 'domain'), 'agentError' => $error]);
    }

    public function repair(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $domain = strtolower($request->string('domain'));
        try {
            $res = (array) $this->agent->call('mail.repair_permissions', ['domain' => $domain], 600);
        } catch (UserFacingException $e) {
            $this->audit->record('mail.repair_permissions', 'server', $domain !== '' ? $domain : 'mail', AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            return $this->redirectWithFailure('/mail/admin/tools', $e);
        }
        $this->audit->record('mail.repair_permissions', 'server', $domain !== '' ? $domain : 'mail', AuditLogger::SUCCESS, ['fixed' => (int) ($res['fixed'] ?? 0)]);
        $skipped = count((array) ($res['skipped'] ?? []));
        return $this->redirectWithSuccess('/mail/admin/tools', sprintf('Checked %d folder(s) and %d file(s); fixed %d.', (int) ($res['dirs'] ?? 0), (int) ($res['files'] ?? 0), (int) ($res['fixed'] ?? 0)) . ($skipped > 0 ? ' Skipped ' . $skipped . ' link(s) or special file(s): ' . implode(', ', array_slice(array_map('strval', (array) $res['skipped']), 0, 5)) . '.' : ''));
    }
}
