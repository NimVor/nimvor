<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Repositories\WebsiteRepository;
use WhaCP\Services\ServerContext;
use WhaCP\Services\SiteMongoService;
use WhaCP\View\View;

/**
 * Site → MongoDB: a customer's own (prefixed) MongoDB databases and users.
 */
final class SiteMongoController extends SiteController
{
    public function __construct(
        View $view,
        SessionInterface $session,
        WebsiteRepository $sites,
        ServerContext $server,
        private readonly SiteMongoService $mongo,
    ) {
        parent::__construct($view, $session, $sites, $server);
    }

    public function index(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $status = null;
        $overview = ['databases' => [], 'users' => []];
        $error = null;
        try {
            $status = $this->mongo->status();
            if ($status['installed'] && $status['running']) {
                $overview = $this->mongo->overview($site);
            }
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('sites/mongo', [
            'site' => $site, 'tab' => 'mongo', 'status' => $status, 'overview' => $overview,
            'prefix' => $this->mongo->prefix($site), 'agentError' => $error,
        ]);
    }

    public function createDatabase(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'mongo', function (array $site) use ($request): string {
            $name = $this->mongo->createDatabase($site, $request->string('name'), $this->userId($request));
            return 'Created database ' . $name . '.';
        });
    }

    public function dropDatabase(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'mongo', function (array $site) use ($request): string {
            $this->mongo->dropDatabase($site, $request->string('name'), $this->userId($request));
            return 'Database dropped.';
        });
    }

    public function createUser(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'mongo', function (array $site) use ($request): string {
            $user = $this->mongo->createUser($site, $request->string('user'), $request->string('password'), $request->string('db'), $request->string('role') ?: 'readWrite', $this->userId($request));
            return 'Created user ' . $user . '.';
        });
    }

    public function setPassword(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'mongo', function (array $site) use ($request): string {
            $this->mongo->setPassword($site, $request->string('user'), $request->string('db'), $request->string('password'), $this->userId($request));
            return 'Password changed.';
        });
    }

    public function dropUser(Request $request, string $id): Response
    {
        return $this->act($request, $id, 'mongo', function (array $site) use ($request): string {
            $this->mongo->dropUser($site, $request->string('user'), $request->string('db'), $this->userId($request));
            return 'User dropped.';
        });
    }

    /** The full-page browser for this website's own (prefixed) databases. */
    public function browser(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $databases = [];
        try {
            if ($this->mongo->status()['running']) {
                $databases = array_values(array_filter(array_map(
                    static fn (array $d): string => (string) ($d['name'] ?? ''),
                    $this->mongo->overview($site)['databases'],
                ), static fn (string $n): bool => $n !== ''));
            }
        } catch (AgentException) {
            // The page still loads; the error surfaces when the browser runs.
        }
        $current = trim((string) $request->query('db', ''));
        if ($current !== '' && !in_array($current, $databases, true)) {
            $current = '';
        }
        return Response::html($this->view->render('server/mongo_browser', [
            'api' => $this->view->url('sites.mongo.browse', ['id' => (int) $site['id']]),
            'export' => $this->view->url('sites.mongo.export', ['id' => (int) $site['id']]),
            'back' => $this->view->url('sites.mongo', ['id' => (int) $site['id']]),
            'databases' => $databases,
            'current' => $current,
            'canDelete' => true,
        ], 'layouts/tool'));
    }

    /**
     * Export a collection of this website's own database as a downloadable
     * file. Confined to the account's prefix like every other browser op.
     */
    public function export(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->siteNotFound();
        }
        $db = trim((string) $request->query('db', ''));
        $collection = trim((string) $request->query('collection', ''));
        $format = $request->query('format', 'json') === 'jsonl' ? 'jsonl' : 'json';
        $back = $this->view->url('sites.mongo.browser', ['id' => (int) $site['id']]) . '?db=' . rawurlencode($db);
        try {
            $out = $this->mongo->export($site, $db, $collection, $format, (string) $request->query('filter', ''), (int) $request->query('limit', 2000), $this->userId($request));
        } catch (\WhaCP\Support\UserFacingException $e) {
            return $this->redirectWithError($back, $e->getMessage());
        }
        return $this->download($db, $collection, $out);
    }

    /** Database browser JSON endpoint, confined to this website's databases. */
    public function browse(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return Response::json(['success' => false, 'error' => ['message' => 'Website not found.']], 404);
        }
        $db = trim((string) $request->input('db'));
        try {
            switch ($request->string('op')) {
                case 'collections':
                    return Response::json(['success' => true, 'data' => ['collections' => $this->mongo->collections($site, $db)]]);
                case 'documents':
                    return Response::json(['success' => true, 'data' => $this->mongo->documents(
                        $site,
                        $db,
                        trim((string) $request->input('collection')),
                        (string) $request->input('filter', ''),
                        (int) $request->input('skip', 0),
                        (int) $request->input('limit', 50),
                    )]);
                case 'delete':
                    $n = $this->mongo->deleteDocument($site, $db, trim((string) $request->input('collection')), (string) $request->input('id'), $this->userId($request));
                    return Response::json(['success' => true, 'data' => ['deleted' => $n]]);
                case 'insert':
                    return Response::json(['success' => true, 'data' => $this->mongo->insertDocument($site, $db, trim((string) $request->input('collection')), (string) $request->input('document'), $this->userId($request))]);
                case 'update':
                    return Response::json(['success' => true, 'data' => $this->mongo->updateDocument($site, $db, trim((string) $request->input('collection')), (string) $request->input('id'), (string) $request->input('document'), $this->userId($request))]);
                case 'aggregate':
                    return Response::json(['success' => true, 'data' => $this->mongo->aggregate($site, $db, trim((string) $request->input('collection')), (string) $request->input('pipeline'), (int) $request->input('limit', 50))]);
                case 'indexes':
                    return Response::json(['success' => true, 'data' => $this->mongo->indexes($site, $db, trim((string) $request->input('collection')))]);
                case 'index_create':
                    return Response::json(['success' => true, 'data' => ['name' => $this->mongo->createIndex($site, $db, trim((string) $request->input('collection')), (string) $request->input('keys'), (string) $request->input('options', '{}'), $this->userId($request))]]);
                case 'index_drop':
                    $this->mongo->dropIndex($site, $db, trim((string) $request->input('collection')), (string) $request->input('name'), $this->userId($request));
                    return Response::json(['success' => true, 'data' => ['dropped' => (string) $request->input('name')]]);
                default:
                    return Response::json(['success' => false, 'error' => ['message' => 'Unknown operation.']], 400);
            }
        } catch (\WhaCP\Support\UserFacingException $e) {
            return Response::json(['success' => false, 'error' => ['message' => $e->getMessage()]], 403);
        } catch (AgentException $e) {
            return Response::json(['success' => false, 'error' => $e->toArray()], $e->status() >= 400 && $e->status() < 600 ? $e->status() : 502);
        }
    }
}
