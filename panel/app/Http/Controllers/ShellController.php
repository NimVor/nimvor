<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\SecurityService;
use NimVor\Services\ShellService;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

/** Security → Shell access (administrators). */
final class ShellController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly ShellService $shell,
        private readonly SecurityService $security,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Only administrators manage shell access.', 'status' => 403], 403);
        }
        $status = null;
        $error = null;
        try {
            $status = $this->shell->status();
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('security/shell', ['tab' => 'shell', 'pending' => $this->security->pending(), 'agentError' => $error, 'status' => $status]);
    }

    public function update(Request $request): Response
    {
        $mode = $request->string('mode');
        $all = $request->input('scope') === 'all';
        $users = $all ? array_column($this->shell->status()['accounts'], 'user') : (array) $request->input('users', []);
        if ($all && $request->string('confirm') !== 'all') {
            return $this->redirectWithError('/security/shell', 'Confirm the change for every account.');
        }
        try {
            $done = $this->shell->set($users, $mode, $this->user($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/security/shell', $e);
        }
        $skipped = array_column(array_filter($done, static fn (array $a): bool => !empty($a['skipped'])), 'user');
        $done = array_values(array_filter($done, static fn (array $a): bool => empty($a['skipped'])));
        $waiting = count(array_filter($done, static fn (array $a): bool => ($a['effective'] ?? '') !== $mode));
        return $this->redirectWithSuccess('/security/shell', sprintf('Shell access set to %s for %d account(s).', $mode, count($done)) . ($waiting > 0 && $mode !== 'disabled' ? " {$waiting} of them have no SSH/SFTP login yet: it takes effect once their login is turned on (FTP / SFTP page)." : '') . ($skipped !== [] ? ' Skipped (their plan does not include SSH): ' . implode(', ', $skipped) . '.' : ''));
    }
}
