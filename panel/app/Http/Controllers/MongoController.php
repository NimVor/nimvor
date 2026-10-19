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
        $overview = ['databases' => [], 'users' => [], 'databases_error' => '', 'users_error' => ''];
        $repository = null;
        $overviewError = null;
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
                // A failure reading the databases/users must not blank the
                // whole page with no explanation — show it in place.
                try {
                    $overview = $this->mongo->overview();
                } catch (AgentException $e) {
                    $overviewError = $e;
                }
                try {
                    $config = $this->mongo->config();
                } catch (AgentException) {
                }
                try {
                    $log = $this->mongo->log();
                } catch (AgentException) {
                }
            }
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('server/mongo', [
            'tab' => 'mongo', 'status' => $status, 'overview' => $overview, 'repository' => $repository,
            'overviewError' => $overviewError, 'agentError' => $error,
            'config' => $config ?? null, 'log' => $log ?? null,
        ]);
    }

    public function config(Request $request): Response
    {
        if (!$this->view->can(P::SERVER_MANAGE)) {
            return $this->redirectWithError('/mongo', 'You may not do this.');
        }
        $port = (int) $request->input('port', 0);
        if ($port < 1 || $port > 65535) {
            return $this->redirectWithError('/mongo', 'Enter a port between 1 and 65535.');
        }
        try {
            $this->mongo->setConfig($port, trim((string) $request->input('bind_ip')), $request->input('authorization') === '1', $this->userId($request));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', 'MongoDB configuration saved and the service restarted.');
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

    /**
     * The full-page database browser (collections sidebar + documents pane).
     * The JSON endpoint it talks to is browse() below.
     */
    public function browser(Request $request): Response
    {
        $databases = [];
        try {
            $databases = array_values(array_filter(array_map(
                static fn ($d): string => (string) ($d['name'] ?? ''),
                $this->mongo->overview()['databases'],
            ), static fn (string $n): bool => $n !== ''));
        } catch (AgentException) {
            // Shown empty; the page still loads and reports the error on use.
        }
        $db = trim((string) $request->query('db', ''));
        if ($db !== '' && !in_array($db, $databases, true)) {
            $db = '';
        }
        return Response::html($this->view->render('server/mongo_browser', [
            'api' => $this->view->url('mongo.browse'),
            'back' => $this->view->url('mongo'),
            'databases' => $databases,
            'current' => $db,
            'canDelete' => $this->view->can(P::SERVER_MANAGE),
        ], 'layouts/tool'));
    }

    /**
     * Database browser (JSON): list collections, read documents, delete one.
     * Reads need server.view; deleting a document needs server.manage.
     */
    public function browse(Request $request): Response
    {
        $op = $request->string('op');
        $db = trim((string) $request->input('db'));
        try {
            switch ($op) {
                case 'collections':
                    return Response::json(['success' => true, 'data' => ['collections' => $this->mongo->collections($db)]]);
                case 'documents':
                    return Response::json(['success' => true, 'data' => $this->mongo->documents(
                        $db,
                        trim((string) $request->input('collection')),
                        (string) $request->input('filter', ''),
                        (int) $request->input('skip', 0),
                        (int) $request->input('limit', 50),
                    )]);
                case 'delete':
                    if (!$this->view->can(P::SERVER_MANAGE)) {
                        return Response::json(['success' => false, 'error' => ['message' => 'You may not do this.']], 403);
                    }
                    $n = $this->mongo->deleteDocument($db, trim((string) $request->input('collection')), (string) $request->input('id'), $this->userId($request));
                    return Response::json(['success' => true, 'data' => ['deleted' => $n]]);
                default:
                    return Response::json(['success' => false, 'error' => ['message' => 'Unknown operation.']], 400);
            }
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], $e->status() >= 400 && $e->status() < 600 ? $e->status() : 502);
        }
    }
}
