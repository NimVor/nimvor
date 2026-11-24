<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\MailAdminService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * Email administration for server administrators: the mail queue,
 * delivery reports and statistics from the mail logs.
 */
final class MailAdminController extends Controller
{
    public const RANGES = MailAdminService::RANGES;

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly MailAdminService $admin,
        private readonly \NimVor\Services\MailService $mail,
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
            $q = $this->admin->queue();
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
            $headers = $this->admin->message($id);
        } catch (AgentException | ValidationException $e) {
            return Response::json(['success' => false, 'error' => ['message' => $e->getMessage()]], 404);
        }
        return Response::json(['success' => true, 'headers' => $headers]);
    }

    public function queueAction(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $action = $request->string('action');
        $ids = array_map('strval', (array) $request->input('ids', []));
        try {
            $res = $this->admin->queueAction($action, $ids);
        } catch (ValidationException $e) {
            return $this->redirectWithError('/mail/admin/queue', implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/mail/admin/queue', $e);
        }
        $n = (int) $res['count'];
        $done = ['delete' => 'deleted', 'retry' => 'sent for delivery', 'hold' => 'put on hold', 'release' => 'released', 'requeue' => 'requeued'];
        return $this->redirectWithSuccess('/mail/admin/queue', $action === 'flush' ? 'Delivery of every deferred message is being retried.' : $n . ' message(s) ' . ($done[$action] ?? $action) . '.');
    }

    public function reports(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $range = (string) $request->query('range', '24h');
        $filters = ['address' => trim((string) $request->query('address', '')), 'status' => (string) $request->query('status', '')];
        $tab = $request->query('view', '') === 'stats' ? 'stats' : 'reports';
        $rep = null;
        $error = null;
        try {
            $rep = $this->admin->report($range, $filters['address'], $filters['status']);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('mail/admin/reports', ['tab' => $tab, 'report' => $rep === null ? null : $rep['report'], 'source' => (string) ($rep['source'] ?? ''), 'range' => $range, 'filters' => $filters, 'agentError' => $error]);
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
            $status = $this->admin->serverStatus();
            $log = $this->admin->logTail(100);
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
            $this->admin->setSettings($pf, $dc);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/mail/admin/server#mail-postfix', $e);
        }
        return $this->redirectWithSuccess('/mail/admin/server#mail-postfix', 'Mail server settings applied.');
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
                $trace = $this->admin->trace($address);
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
        try {
            $res = $this->admin->repair($request->string('domain'));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/mail/admin/tools', $e);
        }
        $skipped = count((array) ($res['skipped'] ?? []));
        return $this->redirectWithSuccess('/mail/admin/tools', sprintf('Checked %d folder(s) and %d file(s); fixed %d.', (int) ($res['dirs'] ?? 0), (int) ($res['files'] ?? 0), (int) ($res['fixed'] ?? 0)) . ($skipped > 0 ? ' Skipped ' . $skipped . ' link(s) or special file(s): ' . implode(', ', array_slice(array_map('strval', (array) $res['skipped']), 0, 5)) . '.' : ''));
    }
}
