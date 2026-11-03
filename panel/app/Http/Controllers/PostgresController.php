<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\PostgresRepository;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Security\Permissions as P;
use NimVor\Services\JobTracker;
use NimVor\Services\PostgresService;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * PostgreSQL: databases, users and privileges for customers (their own) and administrators (all), and the
 * server itself (install, adopt, service) for those who may manage it.
 */
final class PostgresController extends Controller
{
    private const BASE = '/postgres';

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly PostgresService $pg,
        private readonly PostgresRepository $repo,
        private readonly WebsiteRepository $sites,
        private readonly ServerContext $server,
        private readonly JobTracker $jobs,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $user = $this->user($request);
        $status = null;
        $error = null;
        $live = null;
        try {
            $status = $this->pg->status();
            if ($status['installed'] && $status['running'] && $status['managed']) {
                $live = $this->pg->live();
            }
        } catch (AgentException $e) {
            $error = $e;
        }
        $pg = $this->pg->pgServerId();
        $databases = $this->repo->databases($pg, $user);
        $roles = $this->repo->roles($pg, $user);
        $sizes = [];
        foreach ((array) ($live['databases'] ?? []) as $d) {
            $sizes[(string) $d['name']] = (array) $d;
        }
        foreach ($databases as &$d) {
            $d['size_bytes'] = (int) ($sizes[(string) $d['name']]['size_bytes'] ?? 0);
            $d['connections'] = (int) ($sizes[(string) $d['name']]['connections'] ?? 0);
            $d['live'] = isset($sizes[(string) $d['name']]);
        }
        unset($d);
        // Administrators also see what exists on the server without a record here (read-only).
        $unmanaged = [];
        if (Scope::isAll($user) && $live !== null) {
            $known = array_column($this->repo->databases($pg, ['role_scope' => 'all']), 'name');
            foreach ((array) ($live['databases'] ?? []) as $d) {
                $d = (array) $d;
                if (empty($d['system']) && !in_array((string) $d['name'], $known, true)) {
                    $unmanaged[] = $d;
                }
            }
        }
        $siteList = $this->sites->all($this->server->id(), $user);
        return $this->render('postgres/index', [
            'status' => $status, 'agentError' => $error, 'live' => $live, 'databases' => $databases, 'roles' => $roles,
            'unmanaged' => $unmanaged, 'prefix' => $this->pg->prefix($user), 'sites' => $siteList,
            'canServer' => $this->view->can(P::POSTGRES_MANAGE), 'isAdmin' => Scope::isAll($user),
        ]);
    }

    public function install(Request $request): Response
    {
        try {
            $job = $this->pg->install($this->userId($request));
            $this->jobs->track($job, 'Install PostgreSQL', 'server', (string) $this->server->id(), $this->userId($request));
            $this->flashJob($job);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'Installing PostgreSQL…');
    }

    public function adopt(Request $request): Response
    {
        try {
            $this->pg->adopt($request->string('harden') === '1', $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'NimVor now manages the existing PostgreSQL server.');
    }

    public function service(Request $request): Response
    {
        $op = $request->string('op');
        if (!in_array($op, ['start', 'stop', 'restart', 'reload'], true)) {
            return $this->redirectWithError(self::BASE, 'Unknown service operation.');
        }
        try {
            $this->pg->service($op, $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'PostgreSQL: ' . $op . ' done.');
    }

    public function createDatabase(Request $request): Response
    {
        try {
            $r = $this->pg->createDatabase($request->all(), $this->user($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors(self::BASE, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        if (isset($r['password'])) {
            $this->session->flash('secret', ['title' => 'Database user created', 'user' => $r['user'], 'password' => $r['password']]);
        }
        return $this->redirectWithSuccess(self::BASE, 'Database created.');
    }

    public function createUser(Request $request): Response
    {
        try {
            $r = $this->pg->createUser($request->all(), $this->user($request));
        } catch (ValidationException $e) {
            return $this->backWithErrors(self::BASE, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        $this->session->flash('secret', ['title' => 'Database user created', 'user' => $r['user'], 'password' => $r['password']]);
        return $this->redirectWithSuccess(self::BASE, 'Database user created.');
    }

    public function password(Request $request): Response
    {
        $role = $this->repo->role($this->pg->pgServerId(), (int) $request->input('id'), $this->user($request));
        if ($role === null) {
            return $this->redirectWithError(self::BASE, 'Database user not found.');
        }
        try {
            $pw = $this->pg->resetPassword($role, $request->string('store_password') === '1');
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        $this->session->flash('secret', ['title' => 'New password', 'user' => $role['name'], 'password' => $pw]);
        return $this->redirectWithSuccess(self::BASE, 'The password was changed.');
    }

    public function grant(Request $request): Response
    {
        $user = $this->user($request);
        $pg = $this->pg->pgServerId();
        $db = $this->repo->database($pg, (int) $request->input('database_id'), $user);
        $role = $this->repo->role($pg, (int) $request->input('role_id'), $user);
        if ($db === null || $role === null) {
            return $this->redirectWithError(self::BASE, 'Database or user not found.');
        }
        try {
            $this->pg->setGrant($db, $role, $request->string('level'));
        } catch (ValidationException $e) {
            return $this->backWithErrors(self::BASE, $e, $request->all());
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'Access updated.');
    }

    public function dropDatabase(Request $request): Response
    {
        $db = $this->repo->database($this->pg->pgServerId(), (int) $request->input('id'), $this->user($request));
        if ($db === null) {
            return $this->redirectWithError(self::BASE, 'Database not found.');
        }
        try {
            $this->pg->dropDatabase($db, $request->string('confirm'));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'Database deleted.');
    }

    public function dropUser(Request $request): Response
    {
        $role = $this->repo->role($this->pg->pgServerId(), (int) $request->input('id'), $this->user($request));
        if ($role === null) {
            return $this->redirectWithError(self::BASE, 'Database user not found.');
        }
        try {
            $this->pg->dropUser($role);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::BASE, $e);
        }
        return $this->redirectWithSuccess(self::BASE, 'Database user deleted.');
    }
}
