<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\DatabaseRepository;
use NimVor\Services\DbToolsService;
use NimVor\Services\JobTracker;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\View\View;

/**
 * phpMyAdmin and Adminer as alternatives to the built-in database browser. Administrators install and remove them;
 * everyone who manages databases can see which are available.
 */
final class DbToolsController extends Controller
{
    private const PAGE = '/databases/tools';

    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly DbToolsService $tools,
        private readonly JobTracker $jobs,
        private readonly DatabaseRepository $databases,
        private readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    public function index(Request $request): Response
    {
        return $this->render('databases/tools', [
            'status' => $this->tools->status(),
            'isAdmin' => Scope::isAll($this->user($request)),
        ]);
    }

    public function install(Request $request, string $tool): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->redirectWithError(self::PAGE, 'Only administrators can install database tools.');
        }
        try {
            $job = $this->tools->install($tool, $this->userId($request));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::PAGE, $e);
        }
        if ($job !== '') {
            $this->jobs->track($job, 'Install ' . (DbToolsService::TOOLS[$tool] ?? $tool), 'server', 'dbtools', $this->userId($request));
            $this->flashJob($job);
        }
        return $this->redirectWithSuccess(self::PAGE, 'Installing ' . (DbToolsService::TOOLS[$tool] ?? $tool) . '. It is checked against its published checksum and put behind the panel\'s address.');
    }

    public function remove(Request $request, string $tool): Response
    {
        if (!Scope::isAll($this->user($request))) {
            return $this->redirectWithError(self::PAGE, 'Only administrators can remove database tools.');
        }
        try {
            $this->tools->remove($tool);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure(self::PAGE, $e);
        }
        return $this->redirectWithSuccess(self::PAGE, (DbToolsService::TOOLS[$tool] ?? $tool) . ' was removed.');
    }

    /**
     * One-click sign-in: opens the tool already signed in to this database, with an account that reaches this
     * database only (the same rule as the built-in browser: a database the user may manage).
     */
    public function open(Request $request, string $id, string $tool): Response
    {
        $db = $this->databases->database($this->server->id(), (int) $id, $this->user($request));
        if ($db === null) {
            return $this->redirectWithError('/databases', 'This database does not exist or you do not have access to it.');
        }
        try {
            $path = $this->tools->signon($tool, (string) $db['name']);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/databases', $e);
        }
        return Response::redirect($path);
    }
}
