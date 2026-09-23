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
use WhaCP\View\View;

/**
 * PHP version management: install, enable/disable, list modules.
 */
final class PhpController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly AgentClient $agent,
        private readonly JobTracker $jobs,
        private readonly AuditLogger $audit,
    ) {
        parent::__construct($view, $session);
    }

    public function install(Request $request): Response
    {
        $version = $request->string('version');
        if (!preg_match('/^[5-9]\.[0-9]{1,2}$/', $version)) {
            return $this->redirectWithError('/php', 'Choose a PHP version.');
        }
        try {
            $res = (array) $this->agent->call('php.install', ['version' => $version]);
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/php', $e);
        }
        $this->jobs->track((string) $res['job_id'], 'Install PHP ' . $version, 'php', $version, $this->userId($request));
        $this->audit->record('php.install', 'php', $version, AuditLogger::SUCCESS, ['packages' => $res['packages'] ?? []]);
        $this->flashJob((string) $res['job_id']);
        return $this->redirectWithSuccess('/php', 'Installing PHP ' . $version . '. You can follow the progress below.');
    }

    public function toggle(Request $request, string $version): Response
    {
        if (!preg_match('/^[5-9]\.[0-9]{1,2}$/', $version)) {
            return $this->redirectWithError('/php', 'Unknown PHP version.');
        }
        $enable = $request->input('enabled') === '1';
        try {
            $this->agent->call('php.set_enabled', ['version' => $version, 'enabled' => $enable]);
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/php', $e);
        }
        $this->audit->record($enable ? 'php.enable' : 'php.disable', 'php', $version);
        return $this->redirectWithSuccess('/php', 'PHP ' . $version . ($enable ? ' started and enabled.' : ' stopped and disabled.'));
    }

    public function modules(Request $request, string $version): Response
    {
        if (!preg_match('/^[5-9]\.[0-9]{1,2}$/', $version)) {
            return Response::json(['success' => false], 404);
        }
        try {
            return Response::json(['success' => true, 'modules' => $this->agent->call('php.modules', ['version' => $version])]);
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], 502);
        }
    }
}
