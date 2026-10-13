<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Security\Permissions as P;
use WhaCP\Services\JobTracker;
use WhaCP\Services\MongoService;
use WhaCP\Services\ServerContext;
use WhaCP\View\View;

/**
 * Server → MongoDB: the MongoDB server's databases and users. Administrators
 * only (server.view to see, server.manage to change).
 */
final class MongoController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly MongoService $mongo,
        private readonly JobTracker $jobs,
        private readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $status = null;
        $overview = ['databases' => [], 'users' => []];
        $repository = null;
        $error = null;
        try {
            $status = $this->mongo->status();
            if (!$status['installed'] || !$status['running']) {
                // The install section needs to know whether the repository is
                // ready; a failure here must not hide the rest of the page.
                try {
                    $repository = $this->mongo->repository();
                } catch (AgentException) {
                }
            }
            if ($status['installed'] && $status['running']) {
                $overview = $this->mongo->overview();
            }
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('server/mongo', ['tab' => 'mongo', 'status' => $status, 'overview' => $overview, 'repository' => $repository, 'agentError' => $error]);
    }

    public function enableRepository(Request $request): Response
    {
        if (!$this->view->can(P::PACKAGES_MANAGE)) {
            return $this->redirectWithError('/mongo', 'You may not do this.');
        }
        try {
            $job = $this->mongo->enableRepository($this->userId($request));
            $this->jobs->track($job, 'Enable MongoDB repository', 'server', (string) $this->server->id(), $this->userId($request));
            $this->flashJob($job);
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', 'Adding MongoDB’s repository…');
    }

    public function install(Request $request): Response
    {
        if (!$this->view->can(P::PACKAGES_MANAGE)) {
            return $this->redirectWithError('/mongo', 'You may not do this.');
        }
        try {
            $job = $this->mongo->install($this->userId($request));
            $this->jobs->track($job, 'Install MongoDB', 'server', (string) $this->server->id(), $this->userId($request));
            $this->flashJob($job);
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', 'Installing MongoDB…');
    }

    public function service(Request $request): Response
    {
        if (!$this->view->can(P::SERVICES_MANAGE)) {
            return $this->redirectWithError('/mongo', 'You may not do this.');
        }
        $op = (string) $request->input('op');
        if (!in_array($op, ['start', 'stop', 'restart', 'enable'], true)) {
            return $this->redirectWithError('/mongo', 'Unknown service operation.');
        }
        try {
            $this->mongo->service($op, $this->userId($request));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', 'The MongoDB service was ' . ($op === 'enable' ? 'set to start at boot' : $op . 'ed') . '.');
    }

    public function createDatabase(Request $request): Response
    {
        $name = trim((string) $request->input('name'));
        try {
            $this->mongo->createDatabase($name, $this->userId($request));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', 'Created database ' . $name . '.');
    }

    public function dropDatabase(Request $request): Response
    {
        $name = (string) $request->input('name');
        try {
            $this->mongo->dropDatabase($name, (string) $request->input('confirm'), $this->userId($request));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', 'Dropped database ' . $name . '.');
    }

    public function createUser(Request $request): Response
    {
        try {
            $this->mongo->createUser(
                trim((string) $request->input('user')),
                (string) $request->input('password'),
                trim((string) $request->input('db')),
                (string) $request->input('role', 'readWrite'),
                $this->userId($request),
            );
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', 'Created user ' . trim((string) $request->input('user')) . '.');
    }

    public function setPassword(Request $request): Response
    {
        try {
            $this->mongo->setPassword(trim((string) $request->input('user')), trim((string) $request->input('db')), (string) $request->input('password'), $this->userId($request));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', 'Password changed.');
    }

    public function dropUser(Request $request): Response
    {
        $user = (string) $request->input('user');
        try {
            $this->mongo->dropUser($user, (string) $request->input('db'), $this->userId($request));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', 'Dropped user ' . $user . '.');
    }
}
