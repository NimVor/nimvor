<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Repositories\WebsiteRepository;
use NimVor\Services\ServerContext;
use NimVor\Services\WebPolicyService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/**
 * A website's Security headers & cache page: the owner's choices for browser
 * security headers and cache rules (see WebPolicyService).
 */
final class WebPolicyController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly WebPolicyService $policy,
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
        return $this->render('sites/web_policy', ['site' => $site, 'tab' => 'headers', 'state' => $this->policy->state($site), 'agentError' => null]);
    }

    public function saveHeaders(Request $request, string $id): Response
    {
        return $this->change($request, $id, 'headers', fn (array $site): string => $this->policy->saveHeaders($site, $request->all()));
    }

    public function saveCache(Request $request, string $id): Response
    {
        return $this->change($request, $id, 'cache', fn (array $site): string => $this->policy->saveCache($site, $request->all()));
    }

    public function reset(Request $request, string $id): Response
    {
        return $this->change($request, $id, $request->string('section') === 'cache' ? 'cache' : 'headers', fn (array $site): string => $this->policy->reset($site, $request->string('section')));
    }

    /** @param callable(array<string, mixed>): string $do */
    private function change(Request $request, string $id, string $anchor, callable $do): Response
    {
        $site = $this->site($request, $id);
        if ($site === null) {
            return $this->notFound();
        }
        $to = '/sites/' . (int) $site['id'] . '/web-policy#' . $anchor;
        try {
            $msg = $do($site);
        } catch (ValidationException $e) {
            return $this->backWithErrors($to, $e, $request->all());
        } catch (UserFacingException $e) {
            // Includes the agent's refusals: it kept the previous configuration, and so does the panel.
            return $this->redirectWithFailure($to, $e);
        }
        return $this->redirectWithSuccess($to, $msg);
    }
}
