<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentClient;
use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\AuditLogger;
use WhaCP\Services\JobTracker;
use WhaCP\Services\ServerContext;
use WhaCP\Support\UserFacingException;
use WhaCP\View\View;

/**
 * Packages and server settings (host name, time zone, restart).
 */
final class SystemController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly AuditLogger $audit,
        private readonly JobTracker $jobs,
        private readonly ServerContext $server,
        private readonly \WhaCP\Repositories\UserRepository $users,
        private readonly \WhaCP\Security\PasswordHasher $hasher,
        private readonly \WhaCP\Security\PasswordPolicy $policy,
    ) {
        parent::__construct($view, $session);
    }

    public function packages(Request $request): Response
    {
        if ($request->wantsJson()) {
            // The components with their state, for the installation queue.
            try {
                return Response::json(['success' => true, 'packages' => (array) $this->agent->call('packages.list')]);
            } catch (AgentException $e) {
                return Response::json(['success' => false, 'error' => $e->toArray()], 503);
            }
        }
        $data = ['packages' => [], 'updates' => null, 'agentError' => null];
        try {
            $data['packages'] = (array) $this->agent->call('packages.list');
            $data['updates'] = (array) $this->agent->call('packages.updates', [], 150);
        } catch (AgentException $e) {
            $data['agentError'] = $e;
        }
        return $this->render('server/packages', $data);
    }

    /** @param callable(): (string|array{0: string, 1: string}) $fn */
    private function act(string $to, callable $fn): Response
    {
        try {
            $res = $fn();
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        if (is_array($res)) {
            $this->flashJob($res[1]);
            $res = $res[0];
        }
        return $this->redirectWithSuccess($to, $res);
    }

    private function job(Request $request, string $action, array $params, string $label): string
    {
        $job = (string) ((array) $this->agent->call($action, $params))['job_id'];
        $this->jobs->track($job, $label, 'server', (string) $this->server->id(), $this->userId($request));
        $this->audit->record($action, 'server', (string) $this->server->id(), AuditLogger::SUCCESS, $params);
        return $job;
    }

    public function install(Request $request): Response
    {
        if ($request->wantsJson()) {
            // The installation queue: the job starts at once; the agent runs
            // component installs one after another.
            $name = $request->string('name');
            try {
                return Response::json(['success' => true, 'name' => $name, 'job_id' => $this->job($request, 'packages.install', ['name' => $name], 'Install ' . $name)]);
            } catch (AgentException $e) {
                return Response::json(['success' => false, 'error' => $e->toArray()], in_array($e->errorCode(), ['invalid_params', 'conflict'], true) ? 422 : 503);
            }
        }
        return $this->act('/packages', function () use ($request): array {
            $name = $request->string('name');
            return ['Installing ' . $name . '…', $this->job($request, 'packages.install', ['name' => $name], 'Install ' . $name)];
        });
    }

    public function refresh(Request $request): Response
    {
        return $this->act('/packages', fn (): array => ['Refreshing the package index…', $this->job($request, 'packages.refresh', [], 'Refresh package index')]);
    }

    public function upgrade(Request $request): Response
    {
        return $this->act('/packages', fn (): array => ['Installing updates…', $this->job($request, 'packages.upgrade', [], 'Install updates')]);
    }

    public function hostname(Request $request): Response
    {
        return $this->act('/server', function () use ($request): string {
            $h = $request->string('hostname');
            $this->agent->call('server.set_hostname', ['hostname' => $h]);
            $this->audit->record('server.hostname', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['hostname' => $h]);
            return 'Host name changed. Add it to /etc/hosts if tools such as sudo warn that they cannot resolve it.';
        });
    }

    /**
     * The root password: administrators only, confirmed with their own
     * panel password. The password is never logged, stored or returned.
     */
    public function rootPassword(Request $request): Response
    {
        $user = $this->user($request);
        if (($user['role_name'] ?? '') !== 'admin') {
            return $this->redirectWithError('/server', 'Only administrators can change the root password.');
        }
        $current = (string) $request->input('current_password', '');
        $new = (string) $request->input('root_password', '');
        $confirm = (string) $request->input('root_password_confirmation', '');
        $hash = $this->users->passwordHash((int) $user['id']);
        if ($hash === null || !$this->hasher->verify($current, $hash)) {
            $this->audit->record('server.root_password', 'server', (string) $this->server->id(), AuditLogger::DENIED, ['reason' => 'wrong panel password']);
            return $this->redirectWithError('/server#root-password', 'Your panel password is not correct.');
        }
        if ($new !== $confirm) {
            return $this->redirectWithError('/server#root-password', 'The new passwords do not match.');
        }
        $problem = $this->policy->check($new, 'root');
        $classes = (int) preg_match('/[a-z]/', $new) + (int) preg_match('/[A-Z]/', $new) + (int) preg_match('/[0-9]/', $new) + (int) preg_match('/[^A-Za-z0-9]/', $new);
        if ($problem === null && (mb_strlen($new) < 14 || $classes < 3)) {
            $problem = 'Use at least 14 characters with three of: lower case, upper case, digits, symbols.';
        }
        if ($problem === null && (str_contains($new, ':') || preg_match('/[\x00-\x1f]/', $new))) {
            $problem = 'The password must not contain colons or control characters.';
        }
        if ($problem !== null) {
            return $this->redirectWithError('/server#root-password', $problem);
        }
        try {
            $this->agent->call('server.root_password', ['password' => $new]);
        } catch (\WhaCP\Support\UserFacingException $e) {
            $this->audit->record('server.root_password', 'server', (string) $this->server->id(), AuditLogger::FAILURE, ['error' => $e->getMessage()]);
            return $this->redirectWithFailure('/server#root-password', $e);
        }
        $this->audit->record('server.root_password', 'server', (string) $this->server->id());
        return $this->redirectWithSuccess('/server', 'The root password was changed.');
    }

    public function timezone(Request $request): Response
    {
        return $this->act('/server', function () use ($request): string {
            $tz = $request->string('timezone');
            $this->agent->call('server.set_timezone', ['timezone' => $tz]);
            $this->audit->record('server.timezone', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['timezone' => $tz]);
            return 'Time zone changed. Cron jobs and backup schedules now use ' . $tz . '.';
        });
    }

    public function reboot(Request $request): Response
    {
        return $this->act('/server', function () use ($request): string {
            $minutes = max(1, (int) $request->input('minutes', 1));
            $this->agent->call('server.reboot', ['confirm' => $request->string('confirm'), 'minutes' => $minutes]);
            $this->audit->record('server.reboot', 'server', (string) $this->server->id(), AuditLogger::SUCCESS, ['minutes' => $minutes]);
            return 'The server restarts in ' . $minutes . ' minute(s). Websites are unavailable for a short time.';
        });
    }

    public function cancelReboot(Request $request): Response
    {
        return $this->act('/server', function (): string {
            $this->agent->call('server.reboot_cancel');
            $this->audit->record('server.reboot_cancel', 'server', (string) $this->server->id());
            return 'Restart cancelled.';
        });
    }
}
