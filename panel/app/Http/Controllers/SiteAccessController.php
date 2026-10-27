<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Agent\AgentException;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\ServerContext;
use NimVor\Services\SiteAccessService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * A website's Site access page: password-protect the website (or a folder of
 * it) so visitors must sign in with a user name and password.
 */
final class SiteAccessController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly SiteAccessService $access,
        private readonly WebsiteRepository $websites,
        private readonly ServerContext $server,
    ) {
        parent::__construct($view, $session);
    }

    /** @return array<string, mixed>|null */
    private function site(Request $request, string $id): ?array
    {
        return $this->websites->find($this->server->id(), (int) $id, $this->user($request));
    }

    private function notFound(): Response
    {
        return $this->render('errors/error', ['title' => 'Website not found', 'message' => 'This website does not exist or you do not have access to it.', 'status' => 404], 404);
    }

    public function show(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $paths = [];
        $error = null;
        try {
            $paths = $this->access->paths($site);
        } catch (AgentException $e) {
            $error = $e;
        }
        return $this->render('sites/access', ['site' => $site, 'tab' => 'access', 'paths' => $paths, 'agentError' => $error, 'max' => SiteAccessService::MAX_PATHS]);
    }

    public function protect(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $to = '/sites/' . (int) $site['id'] . '/access';
        try {
            $msg = $this->access->protect($site, $request->string('path'), $request->string('user'), (string) $request->input('password', ''), $request->string('realm'), $this->userId($request));
        } catch (ValidationException $e) {
            return $this->redirectWithError($to, implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        return $this->redirectWithSuccess($to, $msg);
    }

    public function remove(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $to = '/sites/' . (int) $site['id'] . '/access';
        try {
            $msg = $this->access->remove($site, $request->string('path'), $this->userId($request));
        } catch (ValidationException $e) {
            return $this->redirectWithError($to, implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        return $this->redirectWithSuccess($to, $msg);
    }
}
