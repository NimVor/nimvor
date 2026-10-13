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
}
