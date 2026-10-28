<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\RedirectsService;
use NimVor\Services\ServerContext;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/** A website's Redirects page: the redirect records, and adding or deleting one. */
final class RedirectsController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly RedirectsService $redirects,
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
        return $this->render('sites/redirects', ['site' => $site, 'tab' => 'redirects', 'redirects' => $this->redirects->list($site), 'max' => RedirectsService::MAX, 'agentError' => null]);
    }

    public function add(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $to = '/sites/' . (int) $site['id'] . '/redirects';
        try {
            $msg = $this->redirects->add($site, $request->all());
        } catch (ValidationException $e) {
            return $this->backWithErrors($to, $e, $request->all());
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
        $to = '/sites/' . (int) $site['id'] . '/redirects';
        try {
            $msg = $this->redirects->remove($site, $request->string('from'));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure($to, $e);
        }
        return $this->redirectWithSuccess($to, $msg);
    }
}
