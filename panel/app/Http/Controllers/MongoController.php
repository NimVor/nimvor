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
                try {
                    $backups = $this->mongo->backups();
                } catch (AgentException) {
                }
            }
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('server/mongo', [
            'tab' => 'mongo', 'status' => $status, 'overview' => $overview, 'repository' => $repository,
            'overviewError' => $overviewError, 'agentError' => $error,
            'config' => $config ?? null, 'log' => $log ?? null, 'backups' => $backups ?? [],
        ]);
    }

    public function config(Request $request): Response
    {
        if (!$this->view->can(P::MONGO_MANAGE)) {
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

    public function configFile(Request $request): Response
    {
        if (!$this->view->can(P::MONGO_MANAGE)) {
            return $this->redirectWithError('/mongo', 'You may not do this.');
        }
        try {
            $this->mongo->writeConfig((string) $request->input('content'), $this->userId($request));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', 'MongoDB configuration file saved and the service restarted.');
    }

    public function adminPassword(Request $request): Response
    {
        if (!$this->view->can(P::MONGO_MANAGE)) {
            return $this->redirectWithError('/mongo', 'You may not do this.');
        }
        try {
            $this->mongo->setAdminPassword((string) $request->input('password'), $this->userId($request));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', 'The MongoDB admin password was set.');
    }

    public function auth(Request $request): Response
    {
        if (!$this->view->can(P::MONGO_MANAGE)) {
            return $this->redirectWithError('/mongo', 'You may not do this.');
        }
        $enable = $request->input('enable') === '1';
        try {
            $this->mongo->setAuthorization($enable, $this->userId($request));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', $enable ? 'Authentication enabled; MongoDB restarted.' : 'Authentication disabled; MongoDB restarted.');
    }

    public function backup(Request $request): Response
    {
        if (!$this->view->can(P::MONGO_MANAGE)) {
            return $this->redirectWithError('/mongo', 'You may not do this.');
        }
        $db = trim((string) $request->input('db'));
        try {
            $r = $this->mongo->backup($db, $this->userId($request));
            $this->jobs->track($r['job_id'], 'Back up MongoDB database ' . $db, 'server', (string) $this->server->id(), $this->userId($request));
            $this->flashJob($r['job_id']);
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', 'Backing up ' . $db . '…');
    }

    public function restore(Request $request): Response
    {
        if (!$this->view->can(P::MONGO_MANAGE)) {
            return $this->redirectWithError('/mongo', 'You may not do this.');
        }
        $db = trim((string) $request->input('db'));
        try {
            $job = $this->mongo->restore((string) $request->input('file'), $db, $request->input('drop') === '1', $this->userId($request));
            $this->jobs->track($job, 'Restore MongoDB database ' . $db, 'server', (string) $this->server->id(), $this->userId($request));
            $this->flashJob($job);
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', 'Restoring ' . $db . '…');
    }

    public function deleteBackup(Request $request): Response
    {
        if (!$this->view->can(P::MONGO_MANAGE)) {
            return $this->redirectWithError('/mongo', 'You may not do this.');
        }
        try {
            $this->mongo->deleteBackup((string) $request->input('file'), $this->userId($request));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', 'Backup deleted.');
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
        $user = trim((string) $request->input('user'));
        try {
            $this->mongo->createDatabase($name, $this->userId($request));
            // When a user name is given, create it in the new database in the
            // same step (a common "set up a database for an app" workflow).
            if ($user !== '') {
                $this->mongo->createUser($user, (string) $request->input('password'), $name, (string) $request->input('role', 'readWrite'), $this->userId($request));
            }
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo', $e);
        }
        return $this->redirectWithSuccess('/mongo', $user !== '' ? 'Created database ' . $name . ' and user ' . $user . '.' : 'Created database ' . $name . '.');
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
            'export' => $this->view->url('mongo.export'),
            'back' => $this->view->url('mongo'),
            'databases' => $databases,
            'current' => $db,
            'canDelete' => $this->view->can(P::MONGO_MANAGE),
        ], 'layouts/tool'));
    }

    /**
     * Export a collection as a downloadable JSON array or JSONL file. A read
     * (server.view); the filter and limit come from the query string so the
     * browser downloads the response directly.
     */
    public function export(Request $request): Response
    {
        $db = trim((string) $request->query('db', ''));
        $collection = trim((string) $request->query('collection', ''));
        $format = $request->query('format', 'json') === 'jsonl' ? 'jsonl' : 'json';
        try {
            $out = $this->mongo->export($db, $collection, $format, (string) $request->query('filter', ''), (int) $request->query('limit', 2000), $this->userId($request));
        } catch (AgentException $e) {
            return $this->redirectWithFailure('/mongo/browser?db=' . rawurlencode($db), $e);
        }
        return $this->download($db, $collection, $out);
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
                case 'coll_create':
                    if (!$this->view->can(P::MONGO_MANAGE)) {
                        return Response::json(['success' => false, 'error' => ['message' => 'You may not do this.']], 403);
                    }
                    $this->mongo->createCollection($db, trim((string) $request->input('collection')), $this->userId($request));
                    return Response::json(['success' => true, 'data' => ['created' => trim((string) $request->input('collection'))]]);
                case 'coll_drop':
                    if (!$this->view->can(P::MONGO_MANAGE)) {
                        return Response::json(['success' => false, 'error' => ['message' => 'You may not do this.']], 403);
                    }
                    $this->mongo->dropCollection($db, trim((string) $request->input('collection')), $this->userId($request));
                    return Response::json(['success' => true, 'data' => ['dropped' => trim((string) $request->input('collection'))]]);
                case 'coll_empty':
                    if (!$this->view->can(P::MONGO_MANAGE)) {
                        return Response::json(['success' => false, 'error' => ['message' => 'You may not do this.']], 403);
                    }
                    return Response::json(['success' => true, 'data' => ['deleted' => $this->mongo->emptyCollection($db, trim((string) $request->input('collection')), $this->userId($request))]]);
                case 'coll_rename':
                    if (!$this->view->can(P::MONGO_MANAGE)) {
                        return Response::json(['success' => false, 'error' => ['message' => 'You may not do this.']], 403);
                    }
                    $this->mongo->renameCollection($db, trim((string) $request->input('collection')), trim((string) $request->input('name')), $this->userId($request));
                    return Response::json(['success' => true, 'data' => ['renamed' => trim((string) $request->input('name'))]]);
                case 'documents':
                    return Response::json(['success' => true, 'data' => $this->mongo->documents(
                        $db,
                        trim((string) $request->input('collection')),
                        (string) $request->input('filter', ''),
                        (int) $request->input('skip', 0),
                        (int) $request->input('limit', 50),
                        (string) $request->input('sort', ''),
                        (string) $request->input('projection', ''),
                    )]);
                case 'delete':
                    if (!$this->view->can(P::MONGO_MANAGE)) {
                        return Response::json(['success' => false, 'error' => ['message' => 'You may not do this.']], 403);
                    }
                    $n = $this->mongo->deleteDocument($db, trim((string) $request->input('collection')), (string) $request->input('id'), $this->userId($request));
                    return Response::json(['success' => true, 'data' => ['deleted' => $n]]);
                case 'insert':
                    if (!$this->view->can(P::MONGO_MANAGE)) {
                        return Response::json(['success' => false, 'error' => ['message' => 'You may not do this.']], 403);
                    }
                    return Response::json(['success' => true, 'data' => $this->mongo->insertDocument($db, trim((string) $request->input('collection')), (string) $request->input('document'), $this->userId($request))]);
                case 'update':
                    if (!$this->view->can(P::MONGO_MANAGE)) {
                        return Response::json(['success' => false, 'error' => ['message' => 'You may not do this.']], 403);
                    }
                    return Response::json(['success' => true, 'data' => $this->mongo->updateDocument($db, trim((string) $request->input('collection')), (string) $request->input('id'), (string) $request->input('document'), $this->userId($request))]);
                case 'aggregate':
                    return Response::json(['success' => true, 'data' => $this->mongo->aggregate($db, trim((string) $request->input('collection')), (string) $request->input('pipeline'), (int) $request->input('limit', 50))]);
                case 'explain':
                    return Response::json(['success' => true, 'data' => $this->mongo->explain($db, trim((string) $request->input('collection')), (string) $request->input('pipeline'))]);
                case 'import':
                    if (!$this->view->can(P::MONGO_MANAGE)) {
                        return Response::json(['success' => false, 'error' => ['message' => 'You may not do this.']], 403);
                    }
                    return Response::json(['success' => true, 'data' => $this->mongo->import($db, trim((string) $request->input('collection')), $request->input('format') === 'jsonl' ? 'jsonl' : 'json', (string) $request->input('content'), $this->userId($request))]);
                case 'schema':
                    return Response::json(['success' => true, 'data' => $this->mongo->schema($db, trim((string) $request->input('collection')), (int) $request->input('sample', 1000))]);
                case 'users':
                    return Response::json(['success' => true, 'data' => ['users' => $this->mongo->databaseUsers($db)]]);
                case 'user_create':
                    if (!$this->view->can(P::MONGO_MANAGE)) {
                        return Response::json(['success' => false, 'error' => ['message' => 'You may not do this.']], 403);
                    }
                    $this->mongo->createUser(trim((string) $request->input('user')), (string) $request->input('password'), $db, (string) $request->input('role', 'readWrite'), $this->userId($request));
                    return Response::json(['success' => true, 'data' => ['user' => trim((string) $request->input('user'))]]);
                case 'user_password':
                    if (!$this->view->can(P::MONGO_MANAGE)) {
                        return Response::json(['success' => false, 'error' => ['message' => 'You may not do this.']], 403);
                    }
                    $this->mongo->setPassword(trim((string) $request->input('user')), $db, (string) $request->input('password'), $this->userId($request));
                    return Response::json(['success' => true, 'data' => ['user' => trim((string) $request->input('user'))]]);
                case 'user_drop':
                    if (!$this->view->can(P::MONGO_MANAGE)) {
                        return Response::json(['success' => false, 'error' => ['message' => 'You may not do this.']], 403);
                    }
                    $this->mongo->dropUser(trim((string) $request->input('user')), $db, $this->userId($request));
                    return Response::json(['success' => true, 'data' => ['dropped' => trim((string) $request->input('user'))]]);
                case 'indexes':
                    return Response::json(['success' => true, 'data' => $this->mongo->indexes($db, trim((string) $request->input('collection')))]);
                case 'index_create':
                    if (!$this->view->can(P::MONGO_MANAGE)) {
                        return Response::json(['success' => false, 'error' => ['message' => 'You may not do this.']], 403);
                    }
                    return Response::json(['success' => true, 'data' => ['name' => $this->mongo->createIndex($db, trim((string) $request->input('collection')), (string) $request->input('keys'), (string) $request->input('options', '{}'), $this->userId($request))]]);
                case 'index_drop':
                    if (!$this->view->can(P::MONGO_MANAGE)) {
                        return Response::json(['success' => false, 'error' => ['message' => 'You may not do this.']], 403);
                    }
                    $this->mongo->dropIndex($db, trim((string) $request->input('collection')), (string) $request->input('name'), $this->userId($request));
                    return Response::json(['success' => true, 'data' => ['dropped' => (string) $request->input('name')]]);
                default:
                    return Response::json(['success' => false, 'error' => ['message' => 'Unknown operation.']], 400);
            }
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], $e->status() >= 400 && $e->status() < 600 ? $e->status() : 502);
        }
    }
}
