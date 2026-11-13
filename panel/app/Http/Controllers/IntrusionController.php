<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\IntrusionService;
use NimVor\Services\SecurityService;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

/** Security → Anti-intrusion (administrators). */
final class IntrusionController extends Controller
{
    private const TABS = ['overview', 'blacklist', 'executions', 'operations'];

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly IntrusionService $intrusion,
        private readonly SecurityService $security,
    ) {
        parent::__construct($view, $session);
    }

    public function show(Request $request): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Only administrators manage anti-intrusion protection.', 'status' => 403], 403);
        }
        $tab = (string) $request->query('tab', 'overview');
        $tab = in_array($tab, self::TABS, true) ? $tab : 'overview';
        $user = (string) $request->query('user', '');
        $status = null;
        $executions = null;
        $error = null;
        try {
            $status = $this->intrusion->status();
            if ($tab === 'executions') {
                $executions = $this->intrusion->executions($user);
            }
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('security/intrusion', [
            'tab' => 'intrusion',
            'sub' => $tab,
            'filterUser' => $user,
            'pending' => $this->security->pending(),
            'agentError' => $error,
            'status' => $status,
            'executions' => $executions,
        ]);
    }

    public function saveAccounts(Request $request): Response
    {
        return $this->act($request, 'overview', fn (): string => $this->intrusion->saveAccounts(
            $request->input('enabled') === '1',
            array_values(array_map('strval', (array) $request->input('log', []))),
            array_values(array_map('strval', (array) $request->input('protect', []))),
            $this->user($request),
        ));
    }

    public function addProgram(Request $request): Response
    {
        $programs = preg_split('/[\s,]+/', (string) $request->input('programs', ''), -1, PREG_SPLIT_NO_EMPTY) ?: [];
        $programs = array_merge($programs, array_map('strval', (array) $request->input('suggested', [])));
        return $this->act($request, 'blacklist', fn (): string => $this->intrusion->addPrograms($programs, $this->user($request)));
    }

    public function removeProgram(Request $request): Response
    {
        return $this->act($request, 'blacklist', fn (): string => $this->intrusion->removeProgram((string) $request->input('path', ''), $this->user($request)));
    }

    private function act(Request $request, string $tab, callable $fn): Response
    {
        $to = '/security/intrusion?tab=' . $tab;
        try {
            return $this->redirectWithSuccess($to, $fn());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
    }
}
