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
    ) {
        parent::__construct($view, $session);
    }

    public function packages(Request $request): Response
    {
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
