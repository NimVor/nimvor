<?php

declare(strict_types=1);

namespace WhaCP\Http\Controllers;

use WhaCP\Agent\AgentException;
use WhaCP\Http\Request;
use WhaCP\Http\Response;
use WhaCP\Http\Session\SessionInterface;
use WhaCP\Services\MongoService;
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
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        $status = null;
        $overview = ['databases' => [], 'users' => []];
        $error = null;
        try {
            $status = $this->mongo->status();
            if ($status['installed'] && $status['running']) {
                $overview = $this->mongo->overview();
            }
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('server/mongo', ['tab' => 'mongo', 'status' => $status, 'overview' => $overview, 'agentError' => $error]);
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
