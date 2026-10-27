<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\AuditLogger;
use NimVor\Services\MailRateService;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

/**
 * Outgoing mail limits (Email administration): recipients per website
 * account and hour, this hour's use, and letting a blocked account send
 * again.
 */
final class MailRateController extends Controller
{
    private const BACK = '/mail/admin/sending-limits';

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly MailRateService $rates,
        private readonly AuditLogger $audit,
    ) {
        parent::__construct($view, $session);
    }

    private function denied(Request $request): ?Response
    {
        return Scope::isAll($this->user($request)) ? null : $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Email administration is for server administrators.', 'status' => 403], 403);
    }

    public function index(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $state = null;
        $error = null;
        try {
            $state = $this->rates->state();
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('mail/admin/sending_limits', ['tab' => 'sending-limits', 'state' => $state, 'agentError' => $error]);
    }

    public function save(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $details = [];
        try {
            $state = $this->rates->state();
            $known = array_map('strval', (array) ($state['accounts'] ?? []));
            $cfg = MailRateService::validate(['enabled' => $request->input('enabled'), 'per_hour' => $request->input('per_hour'), 'accounts' => $request->input('accounts')], $known);
            $details = $cfg;
            $this->rates->save($cfg, !empty($state['config']['enabled']));
        } catch (UserFacingException $e) {
            $this->audit->record('mail.rate_settings', 'server', 'mail-limits', AuditLogger::FAILURE, $details + ['error' => $e->getMessage()]);
            return $this->redirectWithFailure(self::BACK, $e);
        }
        $this->audit->record('mail.rate_settings', 'server', 'mail-limits', AuditLogger::SUCCESS, $details);
        return $this->redirectWithSuccess(self::BACK, $cfg['enabled'] ? 'Mail sending limits saved; they are on.' : 'Mail sending limits saved; they are off.');
    }

    public function reset(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $account = $request->string('account');
        try {
            $this->rates->reset($account);
        } catch (UserFacingException $e) {
            $this->audit->record('mail.rate_reset', 'server', $account !== '' ? $account : 'all', AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            return $this->redirectWithFailure(self::BACK, $e);
        }
        $this->audit->record('mail.rate_reset', 'server', $account !== '' ? $account : 'all', AuditLogger::SUCCESS);
        return $this->redirectWithSuccess(self::BACK, $account !== '' ? $account . ' may send again this hour.' : 'All accounts may send again this hour.');
    }
}
