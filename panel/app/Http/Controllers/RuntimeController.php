<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\JobTracker;
use WhaCP\Services\RuntimeService;
use WhaCP\Services\ServerContext;
use WhaCP\View\View;

/**
 * Server → Runtimes: the Node.js and Python runtimes on the server. An
 * administrator installs extra Node.js versions (used by website workers)
 * and removes managed ones; Python versions are installed under Packages.
 */
final class RuntimeController extends Controller
{
    private const VERSION = '/^\d{1,2}\.\d{1,3}\.\d{1,3}$/';

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly RuntimeService $runtimes,
        private readonly JobTracker $jobs,
        private readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $status = null;
        $error = null;
        try {
            $status = $this->runtimes->status();
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('server/runtimes', ['tab' => 'runtimes', 'status' => $status, 'agentError' => $error]);
    }

    public function install(Request $request): Response
    {
        $version = trim((string) $request->input('version'));
        if (preg_match(self::VERSION, $version) !== 1) {
            return $this->redirectWithError('/runtimes', 'Enter an exact Node.js version, e.g. 20.17.0.');
        }
        try {
            $job = $this->runtimes->installNode($version, $this->userId($request));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/runtimes', $e);
        }
        if ($job !== '') {
            $this->jobs->track($job, 'Install Node.js ' . $version, 'server', (string) $this->server->id(), $this->userId($request));
            $this->flashJob($job);
        }
        return $this->redirectWithSuccess('/runtimes', 'Installing Node.js ' . $version . '. You are notified when it is ready.');
    }

    public function remove(Request $request): Response
    {
        $kind = (string) $request->input('kind');
        $version = (string) $request->input('version');
        if (!in_array($kind, ['node', 'python'], true)) {
            return $this->redirectWithError('/runtimes', 'Unknown runtime.');
        }
        try {
            $this->runtimes->remove($kind, $version, $this->userId($request));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/runtimes', $e);
        }
        return $this->redirectWithSuccess('/runtimes', 'Removed ' . $kind . ' ' . $version . '.');
    }
}
