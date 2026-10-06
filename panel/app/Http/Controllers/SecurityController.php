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
        private readonly \WhaCP\Services\SettingsService $settings,
        private readonly \WhaCP\Services\AuditLogger $audit,
        private readonly \WhaCP\Core\Config $config,
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
        [$hardening] = $err === null ? $this->read('hardening.status') : [null];
        return $this->page('index', [
            'checks' => $err === null ? array_merge($this->panelChecks((array) $hardening), array_map(fn ($c): array => $this->link((array) $c), (array) $checks)) : [],
            'agentError' => $err,
            'logins' => $logins === null ? null : (array) $logins,
            'sshProtected' => $f2b === [] ? null : (!empty($f2b['running']) && $sshJail),
            'panelLogin' => [
                'success' => $this->db->fetch("SELECT username, ip, created_at FROM audit_logs WHERE action = 'auth.login' AND result = 'success' ORDER BY id DESC LIMIT 1"),
                'failure' => $this->db->fetch("SELECT username, ip, created_at FROM audit_logs WHERE action = 'auth.login' AND result <> 'success' ORDER BY id DESC LIMIT 1"),
            ],
        ]);
    }

    /**
     * Checks the panel answers itself: password strength, and the hardening
     * switches (fork bomb protection, traceroute).
     *
     * @param array<string, mixed> $h hardening.status
     * @return list<array<string, mixed>>
     */
    private function panelChecks(array $h): array
    {
        $out = [];
        $min = $this->settings->int('password_min_strength');
        $strong = $this->settings->bool('policy_password_strength') && $min >= \WhaCP\Security\PasswordPolicy::STRONG;
        $out[] = ['id' => 'password_strength', 'title' => 'Password strength requirements', 'severity' => 'medium', 'link' => 'security.password_strength',
            'status' => $strong ? 'pass' : 'warn',
            'detail' => $strong ? sprintf('Password strength requirements are strong (minimum strength %d).', $min) : ($this->settings->bool('policy_password_strength') ? sprintf('The minimum password strength is %d.', $min) : 'Password strength requirements are off.'),
            'remedy' => $strong ? '' : sprintf('Require a password strength of %d or more under Password Strength Configuration.', \WhaCP\Security\PasswordPolicy::STRONG)];
        $hulk = $this->settings->bool('hulk_enabled');
        $out[] = ['id' => 'hulk', 'title' => 'cPHulk Brute Force Protection', 'severity' => 'high', 'link' => 'security.hulk', 'status' => $hulk ? 'pass' : 'fail',
            'detail' => $hulk ? 'cPHulk Brute Force Protection is enabled.' : 'cPHulk Brute Force Protection is disabled.', 'remedy' => $hulk ? '' : 'Enable it under cPHulk Brute Force Protection (add your own IP address to its whitelist first).'];
        if (isset($h['forkbomb'])) {
            $on = !empty($h['forkbomb']['enabled']);
            $out[] = ['id' => 'forkbomb', 'title' => 'Shell Fork Bomb Protection', 'severity' => 'medium', 'link' => 'security.forkbomb', 'status' => $on ? 'pass' : 'warn',
                'detail' => $on ? 'Fork bomb protection is enabled.' : 'Fork bomb protection is disabled.', 'remedy' => $on ? '' : 'Enable it under Shell Fork Bomb Protection.'];
        }
        if (isset($h['traceroute'])) {
            $on = !empty($h['traceroute']['enabled']);
            $none = ($h['traceroute']['programs'] ?? []) === [];
            $out[] = ['id' => 'traceroute', 'title' => 'Traceroute status', 'severity' => 'low', 'link' => 'security.traceroute', 'status' => $none || !$on ? 'pass' : 'warn',
                'detail' => $none ? 'Traceroute is not installed.' : ($on ? 'Traceroute is enabled for every user.' : 'Traceroute is disabled for users other than root.'),
                'remedy' => $none || !$on ? '' : 'Disable it under Traceroute Enable/Disable.'];
        }
        return $out;
    }

    /**
     * The page (or action) that fixes an agent check.
     *
     * @param array<string, mixed> $c
     * @return array<string, mixed>
     */
    private function link(array $c): array
    {
        $links = ['firewall' => 'security.firewall', 'ssh_root' => 'security.ssh', 'ssh_password' => 'security.ssh', 'fail2ban' => 'security.fail2ban', 'updates' => 'packages', 'ssh_version' => 'packages', 'nobody_mail' => 'security.smtp', 'database_version' => 'databases.server', 'package_index' => 'packages', 'dns_recursion' => 'dns', 'database_network' => 'databases.server', 'database_remote_admin' => 'databases.server', 'database_empty_password' => 'databases.server', 'database_global_privileges' => 'databases.server', 'database_grant_option' => 'databases.server', 'database_test' => 'databases.server'];
        if (($c['id'] ?? '') === 'mysql_anonymous' && ($c['status'] ?? '') === 'fail') {
            $c['action'] = ['route' => 'security.mysql_anonymous', 'label' => 'Remove anonymous users'];
        } elseif (isset($links[$c['id'] ?? ''])) {
            $c['link'] = $links[$c['id']];
        }
        return $c;
    }

    public function dropAnonymousUsers(Request $request): Response
    {
        return $this->act('/security', function (): string {
            $res = (array) $this->agent->call('db.drop_anonymous_users', [], 60);
            $removed = array_map('strval', (array) ($res['removed'] ?? []));
            $this->audit->record('db.drop_anonymous_users', 'server', 'mysql', \WhaCP\Services\AuditLogger::SUCCESS, ['hosts' => $removed]);
            return $removed === [] ? 'There were no anonymous users.' : 'Removed the anonymous MySQL users (' . implode(', ', $removed) . ').';
        });
    }

    public function firewall(Request $request): Response
    {
        [$fw, $err] = $this->read('firewall.status');
        /** @var list<string> $proxies */
        $proxies = (array) $this->config->get('security.trusted_proxies', []);
        $ip = $request->ip($proxies);
        return $this->page('firewall', ['fw' => (array) $fw, 'agentError' => $err, 'myIp' => SecurityService::publicAddress($ip) ? $ip : '']);
    }

    public function ssh(Request $request): Response
    {
        [$ssh, $err] = $this->read('ssh.status');
        $keys = [];
        if ($err === null) {
            try {
                $keys = $this->security->sshKeys();
            } catch (AgentException) {
                // Agents before key management: the settings still work.
            }
        }
        return $this->page('ssh', ['ssh' => (array) $ssh, 'sshKeys' => $keys, 'agentError' => $err]);
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
        /** @var list<string> $proxies */
        $proxies = (array) $this->config->get('security.trusted_proxies', []);
        $mine = $request->input('allow_me') === '1' ? $request->ip($proxies) : '';
        return $this->act('/security/firewall', fn (): string => $this->security->setFirewall($request->input('enabled') === '1', $request->input('allow_services', '1') === '1', $mine));
    }

    public function configureSsh(Request $request): Response
    {
        return $this->act('/security/ssh', fn (): string => $this->security->configureSsh($request->all()));
    }

    public function addSshKey(Request $request): Response
    {
        return $this->act('/security/ssh', fn (): string => $this->security->addSshKey($request->string('user'), (string) $request->input('key', ''), $this->user($request)));
    }

    /**
     * The private key is shown on this response only: no redirect, no
     * session, no cache.
     */
    public function generateSshKey(Request $request): Response
    {
        try {
            $key = $this->security->generateSshKey(
                $request->string('user'),
                $request->string('comment'),
                (string) $request->input('passphrase', ''),
                (string) $request->input('passphrase_confirmation', ''),
                $this->user($request),
            );
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/security/ssh', $e);
        }
        [$ssh] = $this->read('ssh.status');
        $port = (string) (((array) ((array) $ssh)['effective'] ?? [])['port'] ?? '22');
        $host = (string) preg_replace('/:\d+$/', '', (string) $request->header('Host'));
        return $this->render('security/ssh_key_created', ['key' => $key, 'sshPort' => $port, 'serverName' => preg_match('/^[A-Za-z0-9.-]+$/', $host) ? $host : 'your-server'])
            ->withHeader('Cache-Control', 'no-store, max-age=0')
            ->withHeader('Pragma', 'no-cache');
    }

    public function removeSshKey(Request $request): Response
    {
        return $this->act('/security/ssh', fn (): string => $this->security->removeSshKey($request->string('user'), $request->string('fingerprint'), $this->user($request)));
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
