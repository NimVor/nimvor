<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Security\Permissions;
use WhaCP\Services\JobTracker;
use WhaCP\Services\RuntimeService;
use WhaCP\Services\ServerContext;
use WhaCP\View\View;

/**
 * Server → Runtimes: the Node.js and Python runtimes on the server. An
 * administrator ticks the stable Node.js and Python versions to install (used
 * by website workers) and removes managed ones.
 */
final class RuntimeController extends Controller
{
    private const VERSIONS = [
        'node' => '/^\d{1,2}\.\d{1,3}\.\d{1,3}$/',
        'python' => '/^3\.\d{1,2}\.\d{1,3}$/',
    ];
    private const LABELS = ['node' => 'Node.js', 'python' => 'Python'];
    private const MAX_AT_ONCE = 10;

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
        $available = null;
        if ($status !== null && $this->view->can(Permissions::SERVER_MANAGE)) {
            try {
                $available = $this->runtimes->available();
            } catch (AgentException) {
                $available = null;
            }
        }
        return $this->render('server/runtimes', ['tab' => 'runtimes', 'status' => $status, 'agentError' => $error, 'available' => $available]);
    }

    /**
     * Installs the ticked versions (versions[]) of one runtime kind, one job
     * each. A single typed "version" is still accepted.
     */
    public function install(Request $request): Response
    {
        $kind = (string) $request->input('kind', 'node');
        if (!isset(self::VERSIONS[$kind])) {
            return $this->redirectWithError('/runtimes', 'Unknown runtime.');
        }
        $label = self::LABELS[$kind];
        $raw = $request->input('versions', []);
        $versions = is_array($raw) ? $raw : [];
        $single = trim((string) $request->input('version'));
        if ($single !== '') {
            $versions[] = $single;
        }
        $versions = array_values(array_unique(array_map(static fn ($v): string => trim((string) $v), $versions)));
        if ($versions === []) {
            return $this->redirectWithError('/runtimes', 'Tick at least one ' . $label . ' version to install.');
        }
        if (count($versions) > self::MAX_AT_ONCE) {
            return $this->redirectWithError('/runtimes', 'Install at most ' . self::MAX_AT_ONCE . ' versions at a time.');
        }
        foreach ($versions as $v) {
            if (preg_match(self::VERSIONS[$kind], $v) !== 1) {
                return $this->redirectWithError('/runtimes', 'That is not a valid ' . $label . ' version.');
            }
        }
        $started = [];
        $failure = null;
        foreach ($versions as $version) {
            try {
                $job = $this->runtimes->install($kind, $version, $this->userId($request));
            } catch (AgentException $e) {
                $failure = $e;
                continue;
            }
            $started[] = $version;
            if ($job !== '') {
                $this->jobs->track($job, 'Install ' . $label . ' ' . $version, 'server', (string) $this->server->id(), $this->userId($request));
                $this->flashJob($job);
            }
        }
        if ($started === []) {
            return $this->redirectWithFailure('/runtimes', $failure);
        }
        $msg = 'Installing ' . $label . ' ' . implode(', ', $started) . '. You are notified when ' . (count($started) > 1 ? 'they are' : 'it is') . ' ready.';
        if ($failure !== null) {
            $msg .= ' Some versions could not be started: ' . $failure->getMessage();
        }
        return $this->redirectWithSuccess('/runtimes', $msg);
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
