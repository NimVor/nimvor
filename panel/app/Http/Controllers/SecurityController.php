<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\SecurityService;
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

final class SecurityController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly SecurityService $security,
        private readonly \WhaCP\Database\Connection $db,
    ) {
        parent::__construct($view, $session);
    }

    /** @return array{0: mixed, 1: AgentException|null} */
    private function read(string $action, int $timeout = 0): array
    {
        try {
            return [$this->agent->call($action, [], $timeout > 0 ? $timeout : null), null];
        } catch (AgentException $e) {
            return [null, $e];
        }
    }

    /** @param array<string, mixed> $data */
    private function page(string $tab, array $data): Response
    {
        return $this->render('security/' . $tab, $data + ['tab' => $tab, 'pending' => $this->security->pending()]);
    }

    public function index(Request $request): Response
    {
        [$checks, $err] = $this->read('security.audit', 120);
        [$logins] = $err === null ? $this->read('ssh.logins', 60) : [null];
        [$f2b] = $err === null ? $this->read('fail2ban.status') : [null];
        $f2b = (array) $f2b;
        $sshJail = false;
        foreach ((array) ($f2b['jails'] ?? []) as $j) {
            $sshJail = $sshJail || (($j['name'] ?? '') === 'sshd');
        }
        return $this->page('index', [
            'checks' => (array) $checks,
            'agentError' => $err,
            'logins' => $logins === null ? null : (array) $logins,
            'sshProtected' => $f2b === [] ? null : (!empty($f2b['running']) && $sshJail),
            'panelLogin' => [
                'success' => $this->db->fetch("SELECT username, ip, created_at FROM audit_logs WHERE action = 'auth.login' AND result = 'success' ORDER BY id DESC LIMIT 1"),
                'failure' => $this->db->fetch("SELECT username, ip, created_at FROM audit_logs WHERE action = 'auth.login' AND result <> 'success' ORDER BY id DESC LIMIT 1"),
            ],
        ]);
    }

    public function firewall(Request $request): Response
    {
        [$fw, $err] = $this->read('firewall.status');
        return $this->page('firewall', ['fw' => (array) $fw, 'agentError' => $err]);
    }

    public function ssh(Request $request): Response
    {
        [$ssh, $err] = $this->read('ssh.status');
        return $this->page('ssh', ['ssh' => (array) $ssh, 'agentError' => $err]);
    }

    public function fail2ban(Request $request): Response
    {
        [$f2b, $err] = $this->read('fail2ban.status');
        return $this->page('fail2ban', ['f2b' => (array) $f2b, 'agentError' => $err]);
    }

    /** @param callable(): string $fn */
    private function act(string $to, callable $fn): Response
    {
        try {
            return $this->redirectWithSuccess($to, $fn());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
    }

    public function addRule(Request $request): Response
    {
        return $this->act('/security/firewall', fn (): string => $this->security->addRule($request->all()));
    }

    public function deleteRule(Request $request): Response
    {
        return $this->act('/security/firewall', fn (): string => $this->security->deleteRule($request->string('spec')));
    }

    public function toggleFirewall(Request $request): Response
    {
        return $this->act('/security/firewall', fn (): string => $this->security->setFirewall($request->input('enabled') === '1'));
    }

    public function configureSsh(Request $request): Response
    {
        return $this->act('/security/ssh', fn (): string => $this->security->configureSsh($request->all()));
    }

    public function configureFail2ban(Request $request): Response
    {
        return $this->act('/security/fail2ban', function () use ($request): string {
            $this->security->configureFail2ban($request->all());
            return 'fail2ban settings tested and applied.';
        });
    }

    public function unban(Request $request): Response
    {
        return $this->act('/security/fail2ban', function () use ($request): string {
            $this->security->unban($request->string('jail'), $request->string('ip'));
            return 'Address unbanned.';
        });
    }

    private function back(Request $request): string
    {
        $to = $request->string('return_to');
        return in_array($to, ['/security', '/security/firewall', '/security/ssh'], true) ? $to : '/security';
    }

    public function confirm(Request $request): Response
    {
        return $this->act($this->back($request), function () use ($request): string {
            $this->security->confirm($request->string('id'));
            return 'Change confirmed and kept.';
        });
    }

    public function undo(Request $request): Response
    {
        return $this->act($this->back($request), function () use ($request): string {
            $this->security->undo($request->string('id'));
            return 'Change undone.';
        });
    }
}
