<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\ServerContext;
use NimVor\Services\SiteEnvService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * A website's environment variables: the Environment page (customers and administrators) and the window of the
 * Websites list. Only websites the viewer may manage are found, so one account never sees another's values.
 */
final class SiteEnvController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly SiteEnvService $env,
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
        return $this->render('sites/env', ['site' => $site, 'tab' => 'env', 'text' => SiteEnvService::toText($this->env->get($site)), 'agentError' => null]);
    }

    /** The window of the Websites list. */
    public function modal(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return Response::html('<div class="modal-header"><h2 class="modal-title h5">Not available</h2><button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button></div><div class="modal-body small">This website does not exist or you do not have access to it.</div>', 404);
        }
        return Response::html($this->view->partial('sites/modal_env', ['site' => $site, 'text' => SiteEnvService::toText($this->env->get($site))]));
    }

    public function update(Request $request, string $id): Response
    {
        $site = $this->site($request, $id);
        $json = $request->wantsJson();
        if ($site === null) {
            return $json ? Response::json(['success' => false, 'error' => ['message' => 'Website not found.']], 404) : $this->notFound();
        }
        $to = '/sites/' . (int) $id . '/env';
        try {
            $message = $this->env->save($site, (string) $request->input('env', ''), $this->user($request));
        } catch (ValidationException $e) {
            if ($json) {
                return Response::json(['success' => false, 'error' => ['message' => (string) (array_values($e->errors())[0] ?? 'Check the variables.'), 'errors' => $e->errors()]], 422);
            }
            return $this->backWithErrors($to, $e, $request->all());
        } catch (UserFacingException $e) {
            return $json ? Response::json(['success' => false, 'error' => ['message' => $e->getMessage()]], 422) : $this->redirectWithFailure($to, $e);
        }
        return $json ? Response::json(['success' => true, 'message' => $message]) : $this->redirectWithSuccess($to, $message);
    }
}
